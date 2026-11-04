package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/spf13/cobra"

	"github.com/filiprojek/dockup/internal/dockerx"
	"github.com/filiprojek/dockup/internal/inventory"
	"github.com/filiprojek/dockup/internal/notify"
	"github.com/filiprojek/dockup/internal/ports"
	"github.com/filiprojek/dockup/internal/render"
	"github.com/filiprojek/dockup/internal/updater"
	"github.com/filiprojek/dockup/internal/updates"
)

func (a *App) dashboard(ctx context.Context) error {
	inv, err := a.Inventory(ctx)
	if err != nil {
		return err
	}
	rep := a.CachedReport()
	if a.JSON {
		return render.JSON(a.Stdout, map[string]any{"summary": inv.Summary(), "updates": rep})
	}
	a.R.Dashboard(a.Stdout, inv.Summary(), rep, a.now())
	return nil
}

func (a *App) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Containers grouped by project with state, health and update status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			inv, err := a.Inventory(cmd.Context())
			if err != nil {
				return err
			}
			rep := a.CachedReport()
			if a.JSON {
				return render.JSON(a.Stdout, map[string]any{"projects": inv.Projects, "summary": inv.Summary(), "updates": rep})
			}
			a.R.Status(a.Stdout, inv, rep)
			return nil
		},
	}
}

func (a *App) checkCmd() *cobra.Command {
	var notifyFlag, cached, all bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check registries for newer images (digest comparison)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var rep updates.Report
			if cached {
				rep = a.CachedReport()
			} else {
				inv, err := a.Inventory(cmd.Context())
				if err != nil {
					return err
				}
				if rep, err = a.Check(cmd.Context(), inv); err != nil {
					return fmt.Errorf("saving cache: %w", err)
				}
			}
			if notifyFlag {
				if m, ok := notify.FromReport(rep); ok {
					senders := a.Senders(a.Cfg.Notify)
					if len(senders) == 0 {
						fmt.Fprintln(a.Stderr, "warning: --notify given but no notify targets configured")
					}
					if err := notify.SendAll(cmd.Context(), senders, m); err != nil {
						fmt.Fprintf(a.Stderr, "warning: notify: %v\n", err)
					}
				}
			}
			if a.JSON {
				return render.JSON(a.Stdout, rep)
			}
			a.R.Updates(a.Stdout, rep, all, a.now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&notifyFlag, "notify", false, "send a notification when updates are available")
	cmd.Flags().BoolVar(&cached, "cached", false, "show the last check instead of querying registries")
	cmd.Flags().BoolVar(&all, "all", false, "also list up-to-date and skipped services")
	return cmd
}

func (a *App) updateCmd() *cobra.Command {
	var all, dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "update [project | project/service]...",
		Short: "Pull and recreate projects (pull, down, up -d) or single services (pull, up -d)",
		Example: `  dockup update immich             # whole project
  dockup update immich/redis gitea  # one service + one project
  dockup update --all --dry-run     # every project with pending updates`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !all {
				return errors.New("name projects/services to update, or use --all (see also: dockup ui)")
			}
			ctx := cmd.Context()
			inv, err := a.Inventory(ctx)
			if err != nil {
				return err
			}
			targets := updater.ParseTargets(args)
			if all {
				rep := a.CachedReport()
				if rep.CheckedAt.IsZero() {
					fmt.Fprintln(a.Stdout, "No cached check, checking registries...")
					if rep, err = a.Check(ctx, inv); err != nil {
						return err
					}
				}
				for _, p := range rep.ProjectsNeedingUpdate() {
					if !slices.ContainsFunc(targets, func(t updater.Target) bool { return t.Project == p }) {
						targets = append(targets, updater.Target{Project: p})
					}
				}
			}
			if len(targets) == 0 {
				fmt.Fprintln(a.Stdout, "Nothing to update.")
				return nil
			}
			return a.Update(ctx, inv, targets, dryRun, yes)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "update every project with pending updates")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "print the commands without running them")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

// Update plans, confirms and applies targets, refreshes the cache for the
// touched projects and prints a recap. With --json, compose output and
// prompts go to stderr and the recap to stdout.
func (a *App) Update(ctx context.Context, inv *inventory.Inventory, targets []updater.Target, dryRun, yes bool) error {
	plans, err := updater.BuildPlans(inv, targets)
	if err != nil {
		return err
	}
	out := a.Stdout
	if a.JSON {
		out = a.Stderr
	}
	if !dryRun && !yes {
		fmt.Fprintln(out, "Will update:")
		for _, t := range targets {
			what := "whole project (pull, down, up -d)"
			if len(t.Services) > 0 {
				what = fmt.Sprintf("services %v (pull, up -d)", t.Services)
			}
			fmt.Fprintf(out, "  %s: %s\n", t.Project, what)
		}
		if !a.confirm(out, "Proceed?") {
			fmt.Fprintln(out, "Aborted.")
			return nil
		}
	}

	u := updater.Updater{Runner: a.Runner, Out: out, DryRun: dryRun, Now: a.Now}
	if isTTY(out) {
		u.Progress = "tty"
	}
	if !dryRun {
		if f, err := updater.OpenLog(a.Cfg.LogPath); err != nil {
			fmt.Fprintf(a.Stderr, "warning: can't write log %s: %v\n", a.Cfg.LogPath, err)
		} else {
			defer f.Close()
			u.Log = f
		}
	}
	outcomes := u.Apply(ctx, plans)
	if dryRun {
		return nil
	}

	after := a.refreshCache(ctx, outcomes)
	recap := updater.BuildRecap(outcomes, inv, after)
	if a.JSON {
		if err := render.JSON(a.Stdout, recap); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(a.Stdout)
		a.R.UpdateRecap(a.Stdout, recap)
	}
	if n := recap.Counts(); n[updater.RecapPartial]+n[updater.RecapFailed] > 0 {
		return fmt.Errorf("%d of %d projects failed", n[updater.RecapPartial]+n[updater.RecapFailed], len(outcomes))
	}
	return nil
}

// refreshCache re-checks only the projects just updated and merges the
// results into the cached report, so the dashboard reflects the update.
// It returns the fresh inventory (empty if it couldn't be loaded).
func (a *App) refreshCache(ctx context.Context, outcomes []updater.Outcome) *inventory.Inventory {
	inv, err := a.Inventory(ctx)
	if err != nil {
		return &inventory.Inventory{}
	}
	touched := map[string]bool{}
	for _, o := range outcomes {
		touched[o.Project] = true
	}
	sub := &inventory.Inventory{}
	for _, p := range inv.Projects {
		if touched[p.Name] {
			sub.Projects = append(sub.Projects, p)
		}
	}
	fresh := updates.Checker{Docker: a.Docker, Registry: a.Registry, Concurrency: a.Cfg.Concurrency}.Check(ctx, sub)
	rep := a.CachedReport()
	if rep.CheckedAt.IsZero() {
		rep.CheckedAt = a.now()
	}
	rep.Results = slices.DeleteFunc(rep.Results, func(r updates.Result) bool { return touched[r.Project] })
	rep.Results = append(rep.Results, fresh.Results...)
	slices.SortStableFunc(rep.Results, func(x, y updates.Result) int { return cmp.Compare(x.Project, y.Project) })
	_ = updates.Save(a.Cfg.CachePath, rep)
	return inv
}

// PortTable collects kernel sockets and Docker-published ports.
func (a *App) PortTable(ctx context.Context) ([]ports.Binding, error) {
	socks, err := ports.ReadListening(a.ProcNet)
	if err != nil {
		return nil, err
	}
	containers, err := a.Docker.Containers(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker: %w", err)
	}
	var published []ports.Published
	byID := map[string]ports.Ref{}
	for _, c := range containers {
		ref := refOf(c)
		byID[c.ID] = ref
		for _, p := range c.Ports {
			if p.Public == 0 {
				continue
			}
			ip := p.IP
			if !ip.IsValid() {
				ip = netip.IPv4Unspecified()
			}
			published = append(published, ports.Published{Proto: ports.Proto(p.Proto), IP: ip, Port: p.Public, Ref: ref})
		}
	}
	ifaces, _ := a.Ifaces()
	return ports.BuildTable(socks, ports.OwnersByInode(a.ProcRoot), published, byID, ifaces), nil
}

func refOf(c dockerx.Container) ports.Ref {
	r := ports.Ref{ContainerID: c.ID, Container: c.Name, Project: c.Project(), Service: c.Service()}
	if r.Project == "" {
		r.Service = c.Name
	}
	return r
}

func (a *App) portsCmd() *cobra.Command {
	var proto, project string
	var managed, group bool
	cmd := &cobra.Command{
		Use:   "ports",
		Short: "List listening ports with owning container/process and interface",
		Example: `  dockup ports                     # every listening socket
  dockup ports --managed --group   # only projects under the compose roots, grouped`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			rows, err := a.PortTable(ctx)
			if err != nil {
				return err
			}
			var declared []ports.Declared
			keep := func(string) bool { return true }
			if managed || group {
				inv, err := a.Inventory(ctx)
				if err != nil {
					return err
				}
				declared = inv.DeclaredStopped()
				if managed {
					m := inv.Managed(a.Cfg.Roots)
					keep = func(p string) bool { return m[p] }
				}
			}
			drop := func(p ports.Proto, proj string) bool {
				return (proto != "" && string(p) != proto) || (project != "" && proj != project) || (managed && !keep(proj))
			}
			rows = slices.DeleteFunc(rows, func(b ports.Binding) bool { return drop(b.Proto, b.Project) })
			declared = slices.DeleteFunc(declared, func(d ports.Declared) bool { return drop(d.Proto, d.Project) })

			if group {
				var filter func(string) bool
				if managed {
					filter = keep
				}
				gs := ports.GroupBindings(rows, declared, filter)
				if a.JSON {
					return render.JSON(a.Stdout, orEmpty(gs))
				}
				a.R.PortGroups(a.Stdout, gs)
				return nil
			}
			if a.JSON {
				return render.JSON(a.Stdout, orEmpty(rows))
			}
			a.R.Ports(a.Stdout, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&proto, "proto", "", "only tcp or udp")
	cmd.Flags().StringVar(&project, "project", "", "only ports of this compose project")
	cmd.Flags().BoolVar(&managed, "managed", false, "only ports of compose projects under the roots (e.g. /srv/docker)")
	cmd.Flags().BoolVar(&group, "group", false, "group by project and service; includes stopped projects' ports as reserved")
	return cmd
}

// orEmpty makes JSON output [] instead of null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func parseProtos(s string) ([]ports.Proto, error) {
	switch s {
	case "tcp":
		return []ports.Proto{ports.TCP}, nil
	case "udp":
		return []ports.Proto{ports.UDP}, nil
	case "both", "":
		return []ports.Proto{ports.TCP, ports.UDP}, nil
	}
	return nil, fmt.Errorf("--proto must be tcp, udp or both")
}

func (a *App) portCheckCmd() *cobra.Command {
	var ipFlag, protoFlag, free string
	cmd := &cobra.Command{
		Use:   "port-check [PORTS]",
		Short: "Check whether ports can be used by a new container (exit 1 if any is in use or reserved)",
		Example: `  dockup port-check 8080
  dockup port-check 8080,8443,9000-9010 --ip 10.8.0.1 --proto tcp
  dockup port-check --free 8000-9000`,
		Args: func(cmd *cobra.Command, args []string) error {
			if (free == "") == (len(args) == 0) {
				return errors.New("give either PORTS or --free RANGE")
			}
			return cobra.MaximumNArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			protos, err := parseProtos(protoFlag)
			if err != nil {
				return err
			}
			ip := netip.IPv4Unspecified()
			if ipFlag != "" {
				if ip, err = netip.ParseAddr(ipFlag); err != nil {
					return fmt.Errorf("--ip: %w", err)
				}
				ip = ip.Unmap()
			}
			chk, err := a.PortChecker(ctx)
			if err != nil {
				return err
			}

			if free != "" {
				lo, hi, err := ports.ParseRange(free)
				if err != nil {
					return err
				}
				p, ok, err := chk.FindFree(protos, ip, lo, hi)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("no free port in %s", free)
				}
				if a.JSON {
					return render.JSON(a.Stdout, map[string]any{"port": p})
				}
				fmt.Fprintln(a.Stdout, p)
				return nil
			}

			list, err := ports.ParseSpec(args[0])
			if err != nil {
				return err
			}
			rs, err := chk.CheckAll(protos, ip, list)
			if err != nil {
				return err
			}
			if a.JSON {
				err = render.JSON(a.Stdout, rs)
			} else {
				a.R.PortCheck(a.Stdout, rs)
			}
			// unknown (couldn't verify) is reported but isn't a conflict
			taken := func(r ports.Result) bool { return r.Status == ports.StatusInUse || r.Status == ports.StatusReserved }
			if err == nil && slices.ContainsFunc(rs, taken) {
				return ExitError{Code: 1}
			}
			return err
		},
	}
	cmd.Flags().StringVar(&ipFlag, "ip", "", "address the container would bind (default 0.0.0.0 = all)")
	cmd.Flags().StringVar(&protoFlag, "proto", "both", "tcp, udp or both")
	cmd.Flags().StringVar(&free, "free", "", "find the first free port in RANGE (e.g. 8000-9000)")
	return cmd
}

// PortChecker assembles a checker from live sockets, Docker and stopped
// compose projects.
func (a *App) PortChecker(ctx context.Context) (*ports.Checker, error) {
	table, err := a.PortTable(ctx)
	if err != nil {
		return nil, err
	}
	inv, err := a.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	return &ports.Checker{Table: table, Declared: inv.DeclaredStopped(), Binder: a.Binder}, nil
}

func (a *App) uiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Interactive terminal UI",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.runTUI(cmd.Context()) },
	}
}

func (a *App) runTUI(ctx context.Context) error {
	if a.TUI == nil {
		return errors.New("interactive UI not available in this build")
	}
	return a.TUI(ctx, a)
}

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version, commit and build date",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if a.JSON {
				return render.JSON(a.Stdout, a.Build)
			}
			fmt.Fprintln(a.Stdout, a.Build.String())
			return nil
		},
	}
}
