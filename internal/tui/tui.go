// Package tui is dockup's interactive terminal UI: browse projects, select
// services to update, run checks and look up ports.
package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/filiprojek/dockup/internal/inventory"
	"github.com/filiprojek/dockup/internal/ports"
	"github.com/filiprojek/dockup/internal/updater"
	"github.com/filiprojek/dockup/internal/updates"
)

// Data is what the UI displays.
type Data struct {
	Inventory *inventory.Inventory
	Report    updates.Report
	Ports     []ports.Binding
	Declared  []ports.Declared // ports of stopped projects
	Managed   map[string]bool  // projects under the compose roots
	Roots     []string
	// CheckOnOpen starts a registry check in the background as soon as the
	// UI opens; the cached report is shown until it finishes.
	CheckOnOpen bool
}

// Funcs are the actions the UI can trigger; nil disables them.
type Funcs struct {
	Check     func() (updates.Report, error)
	PortCheck func(spec string) ([]ports.Result, error)
}

// Result is returned when the program exits.
type Result struct {
	Targets []updater.Target
}

type tab int

const (
	tabUpdates tab = iota
	tabPorts
)

type mode int

const (
	modeBrowse mode = iota
	modeConfirm
	modeInput
)

type row struct {
	project, service string
	updatable        bool
}

type Model struct {
	data  Data
	funcs Funcs
	rows  []row
	sel   map[string]map[string]bool

	tab    tab
	mode   mode
	cursor int
	offset int // first visible row (updates) / port row
	width  int
	height int

	allPorts bool // ports tab: include host processes and unmanaged projects
	flat     bool // ports tab: plain table instead of grouped

	busy  bool
	err   string
	input string
	check []ports.Result

	Result Result
}

type checkDoneMsg struct {
	rep updates.Report
	err error
}

func New(d Data, f Funcs) Model {
	m := Model{data: d, funcs: f, sel: map[string]map[string]bool{}, height: 30, width: 100}
	for _, p := range d.Inventory.Projects {
		m.rows = append(m.rows, row{project: p.Name, updatable: p.Updatable()})
		for _, s := range p.Services {
			m.rows = append(m.rows, row{project: p.Name, service: s.Name, updatable: p.Updatable()})
		}
	}
	m.busy = d.CheckOnOpen && f.Check != nil
	return m
}

func (m Model) Init() tea.Cmd {
	if m.busy {
		return m.checkCmd()
	}
	return nil
}

func (m Model) checkCmd() tea.Cmd {
	check := m.funcs.Check
	return func() tea.Msg {
		rep, err := check()
		return checkDoneMsg{rep, err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case checkDoneMsg:
		m.busy = false
		if msg.err != nil {
			m.err = "check failed: " + msg.err.Error()
		} else {
			m.data.Report, m.err = msg.rep, ""
		}
		return m, nil
	case tea.KeyMsg:
		switch m.mode {
		case modeConfirm:
			return m.updateConfirm(msg)
		case modeInput:
			return m.updateInput(msg), nil
		}
		return m.updateBrowse(msg)
	}
	return m, nil
}

func (m Model) updateConfirm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "enter":
		m.Result.Targets = m.Targets()
		return m, tea.Quit
	case "n", "esc", "q":
		m.mode = modeBrowse
	}
	return m, nil
}

func (m Model) updateInput(k tea.KeyMsg) Model {
	switch k.Type {
	case tea.KeyEsc:
		m.mode = modeBrowse
	case tea.KeyEnter:
		m.mode = modeBrowse
		if m.funcs.PortCheck != nil && m.input != "" {
			rs, err := m.funcs.PortCheck(m.input)
			m.check, m.err = rs, ""
			if err != nil {
				m.err = err.Error()
			}
		}
	case tea.KeyBackspace:
		if m.input != "" {
			m.input = m.input[:len(m.input)-1]
		}
	case tea.KeyRunes:
		m.input += string(k.Runes)
	}
	return m
}

func (m Model) updateBrowse(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.tab = 1 - m.tab
		m.cursor, m.offset = 0, 0
		return m, nil
	}
	if m.tab == tabPorts {
		switch k.String() {
		case "/":
			m.mode, m.input = modeInput, ""
		case "f":
			m.allPorts, m.offset = !m.allPorts, 0
		case "v":
			m.flat, m.offset = !m.flat, 0
		case "j", "down":
			m.offset = min(m.offset+1, max(len(m.portLines())-m.portsHeight(), 0))
		case "k", "up":
			m.offset = max(m.offset-1, 0)
		}
		return m, nil
	}

	switch k.String() {
	case "j", "down":
		m.cursor = min(m.cursor+1, len(m.rows)-1)
	case "k", "up":
		m.cursor = max(m.cursor-1, 0)
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = len(m.rows) - 1
	case " ", "space":
		m.toggle()
	case "a":
		m.selectNeedsUpdate()
	case "x":
		m.sel = map[string]map[string]bool{}
	case "u", "enter":
		if len(m.Targets()) > 0 {
			m.mode = modeConfirm
		}
	case "c":
		if m.funcs.Check != nil && !m.busy {
			m.busy, m.err = true, ""
			return m, m.checkCmd()
		}
	}
	m.scroll()
	return m, nil
}

// scroll keeps the cursor inside the visible window.
func (m *Model) scroll() {
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
}

func (m Model) listHeight() int { return max(m.height-7, 3) }

func (m *Model) set(project, service string, on bool) {
	if m.sel[project] == nil {
		m.sel[project] = map[string]bool{}
	}
	if on {
		m.sel[project][service] = true
	} else {
		delete(m.sel[project], service)
	}
}

func (m *Model) toggle() {
	if len(m.rows) == 0 {
		return
	}
	r := m.rows[m.cursor]
	if !r.updatable {
		return
	}
	if r.service != "" {
		m.set(r.project, r.service, !m.sel[r.project][r.service])
		return
	}
	p := m.data.Inventory.Project(r.project)
	on := !m.fullySelected(p)
	for _, s := range p.Services {
		m.set(p.Name, s.Name, on)
	}
}

func (m Model) fullySelected(p *inventory.Project) bool {
	return len(p.Services) > 0 && len(m.sel[p.Name]) == len(p.Services)
}

func (m *Model) selectNeedsUpdate() {
	for _, r := range m.data.Report.NeedsUpdate() {
		if p := m.data.Inventory.Project(r.Project); p != nil && p.Updatable() {
			m.set(r.Project, r.Service, true)
		}
	}
}

// Targets converts the selection to update targets in inventory order.
func (m Model) Targets() []updater.Target {
	var out []updater.Target
	for i := range m.data.Inventory.Projects {
		p := &m.data.Inventory.Projects[i]
		if len(m.sel[p.Name]) == 0 {
			continue
		}
		if m.fullySelected(p) {
			out = append(out, updater.Target{Project: p.Name})
			continue
		}
		t := updater.Target{Project: p.Name}
		for _, s := range p.Services {
			if m.sel[p.Name][s.Name] {
				t.Services = append(t.Services, s.Name)
			}
		}
		out = append(out, t)
	}
	return out
}

var (
	stTitle   = lipgloss.NewStyle().Bold(true)
	stTab     = lipgloss.NewStyle().Padding(0, 1)
	stTabOn   = stTab.Reverse(true)
	stDim     = lipgloss.NewStyle().Faint(true)
	stCursor  = lipgloss.NewStyle().Reverse(true)
	stGreen   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	stYellow  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stRed     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	stProject = lipgloss.NewStyle().Bold(true)
)

func (m Model) View() string {
	var b strings.Builder
	tabs := []string{"Updates", "Ports"}
	for i, t := range tabs {
		if tab(i) == m.tab {
			b.WriteString(stTabOn.Render(t))
		} else {
			b.WriteString(stTab.Render(t))
		}
	}
	s := m.data.Inventory.Summary()
	fmt.Fprintf(&b, "  %s\n\n", stDim.Render(fmt.Sprintf("%d/%d containers running · %d unhealthy · %d pending updates",
		s.Running, s.Containers, s.Unhealthy, len(m.data.Report.NeedsUpdate()))))

	switch {
	case m.mode == modeConfirm:
		m.viewConfirm(&b)
	case m.tab == tabPorts:
		m.viewPorts(&b)
	default:
		m.viewUpdates(&b)
	}

	if m.err != "" {
		b.WriteString("\n" + stRed.Render(m.err) + "\n")
	}
	if m.busy {
		b.WriteString("\n" + stYellow.Render("checking registries…") + "\n")
	}
	b.WriteString("\n" + stDim.Render(m.help()))
	return b.String()
}

func (m Model) help() string {
	switch {
	case m.mode == modeConfirm:
		return "y confirm · n cancel"
	case m.mode == modeInput:
		return "type ports (8080, 8000-8010) · enter check · esc cancel"
	case m.tab == tabPorts:
		return "/ check port · f all/managed · v grouped/flat · j/k scroll · tab updates · q quit"
	}
	return "space select · a select outdated · x clear · u update · c check registries · tab ports · q quit"
}

func statusText(r updates.Result) string {
	switch r.Status {
	case "":
		return stDim.Render("-")
	case updates.StatusUpToDate:
		return stGreen.Render(string(r.Status))
	case updates.StatusOutdated, updates.StatusPendingRecreate:
		return stYellow.Render(string(r.Status))
	case updates.StatusSkipped:
		return stDim.Render("skipped (" + r.Reason + ")")
	}
	return stRed.Render(string(r.Status))
}

func stateText(s inventory.State) string {
	switch s {
	case inventory.StateRunning:
		return stGreen.Render(string(s))
	case inventory.StatePartial:
		return stYellow.Render(string(s))
	case inventory.StateMissing:
		return stDim.Render(string(s))
	}
	return stRed.Render(string(s))
}

func (m Model) viewUpdates(b *strings.Builder) {
	nameW := 0
	for _, r := range m.rows {
		nameW = max(nameW, len(r.service)+4, len(r.project))
	}
	end := min(m.offset+m.listHeight(), len(m.rows))
	for i := m.offset; i < end; i++ {
		r := m.rows[i]
		p := m.data.Inventory.Project(r.project)
		box := "   "
		if r.updatable {
			box = "[ ]"
			if (r.service == "" && m.fullySelected(p)) || (r.service != "" && m.sel[r.project][r.service]) {
				box = "[x]"
			} else if r.service == "" && len(m.sel[r.project]) > 0 {
				box = "[-]"
			}
		}
		var line string
		if r.service == "" {
			extra := ""
			if !r.updatable {
				extra = stDim.Render("  not updatable (no compose file)")
			}
			line = fmt.Sprintf("%s %s%s", box, stProject.Render(pad(r.project, nameW)), extra)
		} else {
			svc := p.Service(r.service)
			line = fmt.Sprintf("%s     %s  %s  %s", box, pad(r.service, nameW-4), padStyled(stateText(svc.State()), string(svc.State()), 8),
				statusText(m.data.Report.Find(r.project, r.service)))
		}
		if i == m.cursor {
			line = stCursor.Render(line)
		}
		b.WriteString(line + "\n")
	}
}

func (m Model) viewConfirm(b *strings.Builder) {
	b.WriteString(stTitle.Render("Update these?") + "\n\n")
	for _, t := range m.Targets() {
		what := "whole project: pull, down, up -d"
		if len(t.Services) > 0 {
			what = strings.Join(t.Services, ", ") + ": pull, up -d"
		}
		fmt.Fprintf(b, "  %s  %s\n", stProject.Render(t.Project), stDim.Render(what))
	}
}

func (m Model) viewPorts(b *strings.Builder) {
	if m.mode == modeInput {
		fmt.Fprintf(b, "check port: %s█\n\n", m.input)
	}
	for _, r := range m.check {
		st := stGreen.Render("FREE")
		switch r.Status {
		case ports.StatusInUse:
			st = stRed.Render("IN USE")
		case ports.StatusReserved:
			st = stYellow.Render("RESERVED")
		case ports.StatusUnknown:
			st = stYellow.Render("UNKNOWN")
		}
		var who []string
		for _, h := range r.Holders {
			who = append(who, owner(h))
		}
		for _, d := range r.Reserved {
			who = append(who, "stopped "+d.Project+"/"+d.Service)
		}
		fmt.Fprintf(b, "%s/%d  %s  %s\n", r.Proto, r.Port, st, stDim.Render(strings.Join(slices.Compact(who), ", ")+r.Note))
	}
	if len(m.check) > 0 {
		b.WriteString("\n")
	}

	roots := strings.Join(m.data.Roots, ", ")
	scope := "showing projects under " + roots + " · f: show all"
	if m.allPorts {
		scope = "showing all listening ports · f: only projects under " + roots
	}
	b.WriteString(stDim.Render(scope) + "\n\n")

	lines := m.portLines()
	if len(lines) == 0 {
		b.WriteString(stDim.Render("no ports") + "\n")
		return
	}
	start := min(m.offset, len(lines))
	end := min(start+m.portsHeight(), len(lines))
	for _, l := range lines[start:end] {
		b.WriteString(l + "\n")
	}
}

func (m Model) portsHeight() int { return max(m.listHeight()-len(m.check)-3, 3) }

// managed reports whether a project counts as one of the user's (under the roots).
func (m Model) managed(project string) bool { return m.data.Managed[project] }

// portLines renders the ports tab body (grouped or flat, filtered) as lines.
func (m Model) portLines() []string {
	var lines []string
	if m.flat {
		lines = append(lines, stTitle.Render(fmt.Sprintf("%-5s  %-39s  %-5s  %-9s  %s", "PROTO", "ADDRESS", "PORT", "IFACE", "OWNER")))
		for _, p := range m.data.Ports {
			if !m.allPorts && !m.managed(p.Project) {
				continue
			}
			lines = append(lines, fmt.Sprintf("%-5s  %-39s  %-5d  %-9s  %s", p.Proto, p.IP, p.Port, p.Iface, owner(p)))
		}
		return lines
	}

	var keep func(string) bool
	if !m.allPorts {
		keep = m.managed
	}
	groups := ports.GroupBindings(m.data.Ports, m.data.Declared, keep)
	nameW := 0
	for _, g := range groups {
		for _, s := range g.Services {
			nameW = max(nameW, len(s.Name))
		}
	}
	for _, g := range groups {
		switch {
		case g.Project == "":
			lines = append(lines, stProject.Render("other")+stDim.Render(" (host processes, standalone containers)"))
		case g.Stopped:
			lines = append(lines, stProject.Render(g.Project)+"  "+stRed.Render("stopped"))
		default:
			lines = append(lines, stProject.Render(g.Project))
		}
		for _, s := range g.Services {
			for i, e := range s.Entries {
				name := ""
				if i == 0 {
					name = s.Name
				}
				note := ""
				switch {
				case e.Reserved:
					note = stYellow.Render("reserved")
				case e.NATOnly:
					note = stDim.Render("no proxy")
				}
				lines = append(lines, strings.TrimRight(fmt.Sprintf("  %s  %-3s  %-5d  %-16s  %s", pad(name, nameW), e.Proto, e.Port, e.Scope(), note), " "))
			}
		}
	}
	return lines
}

func owner(b ports.Binding) string {
	switch {
	case b.Container != "" && b.Project != "":
		return b.Project + "/" + b.Service
	case b.Container != "":
		return b.Container
	case b.Process != "":
		return fmt.Sprintf("%s (pid %d)", b.Process, b.PID)
	}
	return "?"
}

func pad(s string, w int) string { return s + strings.Repeat(" ", max(w-len(s), 0)) }

func padStyled(styled, raw string, w int) string {
	return styled + strings.Repeat(" ", max(w-len(raw), 0))
}
