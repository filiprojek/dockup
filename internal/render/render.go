// Package render formats dockup data as human-readable tables or JSON.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/filiprojek/dockup/internal/inventory"
	"github.com/filiprojek/dockup/internal/ports"
	"github.com/filiprojek/dockup/internal/updater"
	"github.com/filiprojek/dockup/internal/updates"
)

// Renderer writes human output, optionally with ANSI colour.
type Renderer struct {
	Color bool
}

func Plain() Renderer { return Renderer{} }

const (
	red    = "31"
	green  = "32"
	yellow = "33"
	blue   = "34"
	dim    = "2"
	bold   = "1"
)

func (r Renderer) paint(code, s string) string {
	if !r.Color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// JSON writes v as indented JSON.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// table aligns cells into columns. Section rows are printed verbatim and
// don't affect column widths. Width is measured on the uncoloured text.
type table struct {
	rows []row
}

type row struct {
	section string
	cells   []cell
}

type cell struct{ text, shown string }

func (t *table) add(cells ...cell) { t.rows = append(t.rows, row{cells: cells}) }
func (t *table) section(s string)  { t.rows = append(t.rows, row{section: s}) }

func plain(s string) cell { return cell{s, s} }

func (t *table) write(w io.Writer) {
	var widths []int
	for _, r := range t.rows {
		for i, c := range r.cells {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], utf8.RuneCountInString(c.text))
		}
	}
	for _, r := range t.rows {
		if r.cells == nil {
			fmt.Fprintln(w, r.section)
			continue
		}
		var b strings.Builder
		for i, c := range r.cells {
			b.WriteString(c.shown)
			if i < len(r.cells)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c.text)+2))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

func (r Renderer) c(code, s string) cell { return cell{s, r.paint(code, s)} }

func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Dashboard is the compact overview shown by bare `dockup`.
func (r Renderer) Dashboard(w io.Writer, s inventory.Summary, rep updates.Report, now time.Time) {
	line := fmt.Sprintf("%d/%d running, %d stopped", s.Running, s.Containers, s.Stopped)
	if s.Unhealthy > 0 {
		line += ", " + r.paint(red, fmt.Sprintf("%d unhealthy", s.Unhealthy))
	}
	fmt.Fprintf(w, "%s  %s\n", r.paint(bold, "Containers"), line)
	fmt.Fprintf(w, "%s    %d/%d running\n\n", r.paint(bold, "Projects"), s.ProjectsRunning, s.Projects)

	fmt.Fprint(w, r.paint(bold, "Updates")+"     ")
	switch need := rep.NeedsUpdate(); {
	case rep.CheckedAt.IsZero():
		fmt.Fprintln(w, "never checked, run `dockup check`")
	case len(need) == 0:
		fmt.Fprintf(w, "%s (checked %s)\n", r.paint(green, "all up to date"), ago(rep.CheckedAt, now))
	default:
		projects := rep.ProjectsNeedingUpdate()
		fmt.Fprintf(w, "%s in %s (checked %s)\n", r.paint(yellow, fmt.Sprintf("%d pending", len(need))), plural(len(projects), "project", "projects"), ago(rep.CheckedAt, now))
		byProject := map[string][]string{}
		width := 0
		for _, x := range need {
			byProject[x.Project] = append(byProject[x.Project], x.Service)
			width = max(width, len(x.Project))
		}
		for _, p := range projects {
			fmt.Fprintf(w, "  %-*s  %s\n", width, p, strings.Join(byProject[p], ", "))
		}
	}
	if n := rep.Counts()[updates.StatusError]; n > 0 {
		fmt.Fprintf(w, "  %s\n", r.paint(red, fmt.Sprintf("! %s could not be checked (dockup check --cached)", plural(n, "service", "services"))))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, r.paint(dim, "dockup status | check | update | ports | port-check | ui"))
}

func projectState(p inventory.Project) string {
	running, total := 0, 0
	for _, s := range p.Services {
		total++
		switch s.State() {
		case inventory.StateRunning:
			running++
		case inventory.StatePartial:
			return "partial"
		}
	}
	switch {
	case running == 0:
		return "stopped"
	case running < total:
		return "partial"
	}
	return "running"
}

func (r Renderer) stateCell(s string) cell {
	switch s {
	case "running":
		return r.c(green, s)
	case "partial":
		return r.c(yellow, s)
	case "missing":
		return r.c(dim, s)
	}
	return r.c(red, s)
}

func (r Renderer) healthCell(h string) cell {
	switch h {
	case "":
		return r.c(dim, "-")
	case "healthy":
		return r.c(green, h)
	case "unhealthy":
		return r.c(red, h)
	}
	return r.c(yellow, h)
}

func (r Renderer) updateCell(res updates.Result) cell {
	switch res.Status {
	case "":
		return r.c(dim, "-")
	case updates.StatusUpToDate:
		return r.c(green, string(res.Status))
	case updates.StatusOutdated, updates.StatusPendingRecreate:
		return r.c(yellow, string(res.Status))
	case updates.StatusSkipped:
		return r.c(dim, "skipped ("+res.Reason+")")
	}
	return r.c(red, string(res.Status))
}

// Status lists every project with its services, state, health and update status.
func (r Renderer) Status(w io.Writer, inv *inventory.Inventory, rep updates.Report) {
	var t table
	for _, p := range inv.Projects {
		where := p.Dir
		switch {
		case p.Standalone:
			where = "standalone"
		case p.LoadErr != "":
			where += "  " + r.paint(red, "compose error: "+p.LoadErr)
		}
		t.section(fmt.Sprintf("%s %s %s %s %s", r.paint(bold, p.Name), r.paint(dim, "·"), r.stateCell(projectState(p)).shown, r.paint(dim, "·"), where))
		for _, s := range p.Services {
			t.add(plain("  "+s.Name), r.stateCell(string(s.State())), r.healthCell(health(s)), r.updateCell(rep.Find(p.Name, s.Name)), r.c(dim, image(s)))
		}
	}
	t.write(w)
}

// image prefers the compose image, falling back to what the container runs
// (build-only services have no image in compose).
func image(s inventory.Service) string {
	if s.Image == "" && len(s.Containers) > 0 {
		return s.Containers[0].Image
	}
	return s.Image
}

func health(s inventory.Service) string {
	h := ""
	for _, c := range s.Containers {
		if c.Health == "unhealthy" {
			return c.Health
		}
		if c.Health != "" {
			h = c.Health
		}
	}
	return h
}

// Updates lists services needing attention (or all, with showAll).
func (r Renderer) Updates(w io.Writer, rep updates.Report, showAll bool, now time.Time) {
	if rep.CheckedAt.IsZero() {
		fmt.Fprintln(w, "No update check yet, run `dockup check`.")
		return
	}
	counts := rep.Counts()
	var parts []string
	for _, s := range []updates.Status{updates.StatusOutdated, updates.StatusPendingRecreate, updates.StatusUpToDate, updates.StatusSkipped, updates.StatusError} {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	fmt.Fprintf(w, "Checked %s: %s\n\n", ago(rep.CheckedAt, now), strings.Join(parts, ", "))

	var t table
	last := ""
	for _, x := range rep.Results {
		if !showAll && (x.Status == updates.StatusUpToDate || x.Status == updates.StatusSkipped) {
			continue
		}
		if x.Project != last {
			t.section(r.paint(bold, x.Project))
			last = x.Project
		}
		detail := x.Image
		if x.Status == updates.StatusError {
			detail = x.Reason
		}
		st := r.updateCell(x)
		if x.Status == updates.StatusSkipped {
			st = r.c(dim, string(x.Status))
			detail = x.Reason
		}
		t.add(plain("  "+x.Service), st, r.c(dim, detail))
	}
	if len(t.rows) == 0 {
		fmt.Fprintln(w, r.paint(green, "Everything is up to date."))
		return
	}
	t.write(w)
}

func owner(b ports.Binding) string {
	switch {
	case b.Container != "":
		s := b.Project + "/" + b.Service
		if b.Project == "" {
			s = b.Container
		} else if b.Container != b.Service {
			s += " (" + b.Container + ")"
		}
		return s
	case b.Process != "":
		return fmt.Sprintf("%s (pid %d)", b.Process, b.PID)
	}
	return "?"
}

// Ports renders the listening-port table.
func (r Renderer) Ports(w io.Writer, rows []ports.Binding) {
	var t table
	t.add(r.c(bold, "PROTO"), r.c(bold, "ADDRESS"), r.c(bold, "PORT"), r.c(bold, "IFACE"), r.c(bold, "OWNER"))
	unknown, nat := false, false
	for _, b := range rows {
		o := owner(b)
		oc := plain(o)
		switch {
		case o == "?":
			unknown = true
			oc = r.c(dim, o)
		case b.Container != "":
			oc = r.c(blue, o)
		}
		if b.Source == ports.SourceDocker {
			nat = true
			oc = cell{o + " *", oc.shown + " *"}
		}
		t.add(plain(string(b.Proto)), plain(b.IP.String()), plain(fmt.Sprint(b.Port)), plain(b.Iface), oc)
	}
	t.write(w)
	if unknown || nat {
		fmt.Fprintln(w)
	}
	if unknown {
		fmt.Fprintln(w, r.paint(dim, "? owner not visible to this user; run with sudo to see all processes"))
	}
	if nat {
		fmt.Fprintln(w, r.paint(dim, "* published by Docker without a listening socket (userland-proxy disabled)"))
	}
}

// PortCheck renders availability results.
func (r Renderer) PortCheck(w io.Writer, rs []ports.Result) {
	var t table
	for _, x := range rs {
		var st cell
		var detail string
		switch x.Status {
		case ports.StatusFree:
			st = r.c(green, "FREE")
		case ports.StatusInUse:
			st = r.c(red, "IN USE")
			var hs []string
			for _, h := range x.Holders {
				hs = append(hs, owner(h)+" on "+h.IP.String())
			}
			detail = strings.Join(hs, "; ")
		case ports.StatusReserved:
			st = r.c(yellow, "RESERVED")
			var ds []string
			for _, d := range x.Reserved {
				ds = append(ds, d.Project+"/"+d.Service)
			}
			detail = "declared by stopped " + strings.Join(ds, ", ")
		default:
			st = r.c(yellow, "UNKNOWN")
		}
		if x.Note != "" {
			detail = strings.TrimPrefix(detail+"; "+x.Note, "; ")
		}
		t.add(plain(fmt.Sprintf("%s/%d", x.Proto, x.Port)), plain(x.IP.String()), st, r.c(dim, detail))
	}
	t.write(w)
}

// PortGroups renders ports grouped by project → service.
func (r Renderer) PortGroups(w io.Writer, gs []ports.Group) {
	var t table
	for _, g := range gs {
		switch {
		case g.Project == "":
			t.section(r.paint(bold, "other") + r.paint(dim, " (host processes, standalone containers)"))
		case g.Stopped:
			t.section(fmt.Sprintf("%s %s %s", r.paint(bold, g.Project), r.paint(dim, "·"), r.paint(red, "stopped")))
		default:
			t.section(r.paint(bold, g.Project))
		}
		for _, s := range g.Services {
			for i, e := range s.Entries {
				name := ""
				if i == 0 {
					name = s.Name
				}
				note := cell{}
				switch {
				case e.Reserved:
					note = r.c(yellow, "reserved")
				case e.NATOnly:
					note = r.c(dim, "no proxy")
				}
				t.add(plain("  "+name), plain(string(e.Proto)), plain(fmt.Sprint(e.Port)), plain(e.Scope()), note)
			}
		}
	}
	t.write(w)
}

// UpdateRecap is the end-of-run summary of `dockup update`, one line per
// project (like Ansible's PLAY RECAP), with the reason under failed ones.
func (r Renderer) UpdateRecap(w io.Writer, rec updater.Recap) {
	fmt.Fprintln(w, r.paint(bold, "UPDATE RECAP "+strings.Repeat("─", 50)))
	var t table
	for _, p := range rec.Projects {
		var st cell
		switch p.Status {
		case updater.RecapOK:
			st = r.c(green, string(p.Status))
		case updater.RecapPartial:
			st = r.c(yellow, string(p.Status))
		default:
			st = r.c(red, string(p.Status))
		}
		var steps []string
		for _, s := range p.Steps {
			mark := r.paint(green, "✓")
			switch {
			case !s.Ran:
				mark = r.paint(dim, "–")
			case s.Err != nil:
				mark = r.paint(red, "✗")
			}
			steps = append(steps, s.Name+" "+mark)
		}
		stepsText := ansiRe.ReplaceAllString(strings.Join(steps, " "), "")
		running := fmt.Sprintf("%d/%d running", p.Running, p.Total)
		runCell := plain(running)
		if p.Running < p.Total {
			runCell = r.c(yellow, running)
		}
		changed := r.c(dim, "unchanged")
		if len(p.Updated) > 0 {
			changed = plain("updated: " + strings.Join(p.Updated, ", "))
		}
		if len(p.Steps) > 0 && p.Steps[0].Retried {
			changed = cell{changed.text + " · pull retried", changed.shown + r.paint(dim, " · pull retried")}
		}
		t.add(r.c(bold, p.Project), st, cell{stepsText, strings.Join(steps, " ")}, runCell, plain(p.Duration.Round(time.Second).String()), changed)
		if p.Error != "" {
			t.section("  " + r.paint(red, "✗ "+p.Error))
		}
	}
	t.write(w)

	c := rec.Counts()
	fmt.Fprintf(w, "\n%s · %s · %s · %s · %s\n",
		r.paint(green, fmt.Sprintf("%d ok", c[updater.RecapOK])),
		r.paint(yellow, fmt.Sprintf("%d partial", c[updater.RecapPartial])),
		r.paint(red, fmt.Sprintf("%d failed", c[updater.RecapFailed])),
		plural(rec.UpdatedServices(), "service updated", "services updated"),
		rec.Duration.Round(time.Second))
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)
