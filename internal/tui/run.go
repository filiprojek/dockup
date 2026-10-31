package tui

import (
	"bufio"
	"context"
	"fmt"
	"net/netip"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/filiprojek/dockup/internal/cli"
	"github.com/filiprojek/dockup/internal/ports"
	"github.com/filiprojek/dockup/internal/updates"
)

// Run is the cli.Deps.TUI hook. The UI only selects targets; updates run in
// the normal terminal so compose output streams as usual, then the UI returns.
func Run(ctx context.Context, a *cli.App) error {
	in := bufio.NewReader(a.Stdin)
	for {
		inv, err := a.Inventory(ctx)
		if err != nil {
			return err
		}
		portRows, err := a.PortTable(ctx)
		if err != nil {
			return err
		}
		m := New(Data{Inventory: inv, Report: a.CachedReport(), Ports: portRows}, Funcs{
			Check: func() (updates.Report, error) {
				fresh, err := a.Inventory(ctx)
				if err != nil {
					return updates.Report{}, err
				}
				return a.Check(ctx, fresh)
			},
			PortCheck: func(spec string) ([]ports.Result, error) {
				list, err := ports.ParseSpec(spec)
				if err != nil {
					return nil, err
				}
				chk, err := a.PortChecker(ctx)
				if err != nil {
					return nil, err
				}
				return chk.CheckAll([]ports.Proto{ports.TCP, ports.UDP}, netip.IPv4Unspecified(), list)
			},
		})

		final, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
		if err != nil {
			return err
		}
		targets := final.(Model).Result.Targets
		if targets == nil {
			return nil
		}
		if err := a.Update(ctx, inv, targets, false, true); err != nil {
			fmt.Fprintln(a.Stderr, "dockup:", err)
		}
		fmt.Fprint(a.Stdout, "\nPress Enter to return to dockup, or q + Enter to quit: ")
		line, err := in.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "q" {
			return nil
		}
	}
}
