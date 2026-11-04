// Package cli wires dockup's packages into cobra commands.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/filiprojek/dockup/internal/config"
	"github.com/filiprojek/dockup/internal/dockerx"
	"github.com/filiprojek/dockup/internal/inventory"
	"github.com/filiprojek/dockup/internal/notify"
	"github.com/filiprojek/dockup/internal/ports"
	"github.com/filiprojek/dockup/internal/registry"
	"github.com/filiprojek/dockup/internal/render"
	"github.com/filiprojek/dockup/internal/updater"
	"github.com/filiprojek/dockup/internal/updates"
	"github.com/filiprojek/dockup/internal/version"
)

// Deps are dockup's external dependencies; tests substitute fakes.
type Deps struct {
	Docker   dockerx.Client
	Registry registry.Digester
	Runner   updater.Runner
	Binder   ports.Binder
	ProcNet  string // normally /proc/net
	ProcRoot string // normally /proc
	Ifaces   func() ([]ports.Iface, error)
	Now      func() time.Time
	Senders  func(config.Notify) []notify.Sender
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer

	// Build describes this binary for --version / `dockup version`.
	Build version.Info

	// TUI launches the interactive UI; nil disables `dockup ui`.
	TUI func(ctx context.Context, a *App) error

	roots []string // test override for config roots
}

// HostDeps returns real dependencies. Docker connection errors surface on first use.
func HostDeps() (*Deps, error) {
	eng, err := dockerx.NewEngine()
	if err != nil {
		return nil, err
	}
	return &Deps{
		Docker:   eng,
		Registry: registry.Remote{},
		Runner:   updater.ExecRunner{},
		Binder:   ports.SysBinder{},
		ProcNet:  "/proc/net",
		ProcRoot: "/proc",
		Ifaces:   ports.HostIfaces,
		Now:      time.Now,
		Senders:  notify.FromConfig,
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	}, nil
}

// ExitError carries a process exit code without extra output.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// App is the per-invocation state shared by commands (and the TUI).
type App struct {
	*Deps
	Cfg  config.Config
	JSON bool
	R    render.Renderer
}

func NewRoot(d *Deps) *cobra.Command {
	a := &App{Deps: d}
	var cfgPath string
	var rootDirs []string
	var interactive bool

	root := &cobra.Command{
		Use:           "dockup",
		Short:         "Overview, updates and ports for docker compose projects",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			if cfgPath != "" {
				a.Cfg, err = config.Load(cfgPath)
			} else {
				a.Cfg, err = config.Discover(config.SearchPaths())
			}
			if err != nil {
				return err
			}
			switch {
			case len(rootDirs) > 0:
				a.Cfg.Roots = rootDirs
			case d.roots != nil:
				a.Cfg.Roots = d.roots
			}
			a.R = render.Renderer{Color: isTTY(d.Stdout) && os.Getenv("NO_COLOR") == ""}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if interactive {
				return a.runTUI(cmd.Context())
			}
			return a.dashboard(cmd.Context())
		},
	}
	if d.Build.Version == "" {
		d.Build = version.Resolve("", "", "", nil)
	}
	root.Version = d.Build.String()
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetIn(d.Stdin)
	root.SetOut(d.Stdout)
	root.SetErr(d.Stderr)
	root.Flags().BoolVarP(&interactive, "interactive", "i", false, "start the interactive UI (same as `dockup ui`)")
	pf := root.PersistentFlags()
	pf.BoolVar(&a.JSON, "json", false, "machine-readable JSON output")
	pf.StringVar(&cfgPath, "config", "", "config file (default ~/.config/dockup/config.yml, then /etc/dockup/config.yml)")
	pf.StringSliceVar(&rootDirs, "root", nil, "compose roots to scan (overrides config; default /srv/docker)")

	root.AddCommand(a.statusCmd(), a.checkCmd(), a.updateCmd(), a.portsCmd(), a.portCheckCmd(), a.uiCmd(), a.versionCmd())
	return root
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Inventory scans Docker and the compose roots.
func (a *App) Inventory(ctx context.Context) (*inventory.Inventory, error) {
	b := inventory.Builder{Docker: a.Docker, Roots: a.Cfg.Roots, Ignore: a.Cfg.IsIgnored}
	inv, err := b.Build(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker: %w", err)
	}
	return inv, nil
}

// CachedReport loads the last update check.
func (a *App) CachedReport() updates.Report {
	rep, err := updates.Load(a.Cfg.CachePath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "warning: ignoring unreadable cache %s: %v\n", a.Cfg.CachePath, err)
	}
	return rep
}

// Check queries registries for the inventory and stores the report.
func (a *App) Check(ctx context.Context, inv *inventory.Inventory) (updates.Report, error) {
	rep := updates.Checker{Docker: a.Docker, Registry: a.Registry, Concurrency: a.Cfg.Concurrency}.Check(ctx, inv)
	rep.CheckedAt = a.now()
	return rep, updates.Save(a.Cfg.CachePath, rep)
}

func (a *App) confirm(w io.Writer, prompt string) bool {
	fmt.Fprintf(w, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(a.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
