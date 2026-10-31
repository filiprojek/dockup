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
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	deps, err := cli.HostDeps()
	if deps != nil {
		deps.TUI = tui.Run
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "dockup:", err)
		os.Exit(1)
	}
	root := cli.NewRoot(deps)
	root.Version = version
	if err := root.ExecuteContext(ctx); err != nil {
		var ec cli.ExitError
		if errors.As(err, &ec) {
			os.Exit(ec.Code)
		}
		fmt.Fprintln(os.Stderr, "dockup:", err)
		os.Exit(1)
	}
}
