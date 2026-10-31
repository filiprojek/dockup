// Command dockup shows status, updates and ports for docker compose projects.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/filiprojek/dockup/internal/cli"
	"github.com/filiprojek/dockup/internal/tui"
	buildinfo "github.com/filiprojek/dockup/internal/version"
)

// set via -ldflags by the Makefile / GoReleaser
var (
	version = "dev"
	commit  string
	date    string
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	deps, err := cli.HostDeps()
	if deps != nil {
		deps.TUI = tui.Run
		deps.Build = buildinfo.Current(version, commit, date)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "dockup:", err)
		os.Exit(1)
	}
	if err := cli.NewRoot(deps).ExecuteContext(ctx); err != nil {
		var ec cli.ExitError
		if errors.As(err, &ec) {
			os.Exit(ec.Code)
		}
		fmt.Fprintln(os.Stderr, "dockup:", err)
		os.Exit(1)
	}
}
