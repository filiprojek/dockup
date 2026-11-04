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
	// Check registries when the UI first opens. After an update the touched
	// projects are re-checked by a.Update, so later rounds use the cache.
	checkOnOpen := true
	for {
		inv, err := a.Inventory(ctx)
		if err != nil {
			return err
		}
		portRows, err := a.PortTable(ctx)
		if err != nil {
			return err
		}
		// a check still running when the UI closes is cancelled (and not saved)
		uiCtx, cancel := context.WithCancel(ctx)
		m := New(Data{
			Inventory:   inv,
			Report:      a.CachedReport(),
			Ports:       portRows,
			Declared:    inv.DeclaredStopped(),
			Managed:     inv.Managed(a.Cfg.Roots),
			Roots:       a.Cfg.Roots,
			CheckOnOpen: checkOnOpen,
		}, Funcs{
			Check: func() (updates.Report, error) {
				fresh, err := a.Inventory(uiCtx)
				if err != nil {
					return updates.Report{}, err
				}
				return a.Check(uiCtx, fresh)
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

		checkOnOpen = false
		final, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
		cancel()
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
