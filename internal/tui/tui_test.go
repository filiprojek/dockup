package tui

import (
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/filiprojek/dockup/internal/dockerx"
	"github.com/filiprojek/dockup/internal/inventory"
	"github.com/filiprojek/dockup/internal/ports"
	"github.com/filiprojek/dockup/internal/updater"
	"github.com/filiprojek/dockup/internal/updates"
)

func running() []dockerx.Container { return []dockerx.Container{{State: "running"}} }

func data() Data {
	inv := &inventory.Inventory{Projects: []inventory.Project{
		{Name: "gitea", ComposeFiles: []string{"x"}, Services: []inventory.Service{{Name: "server", Containers: running()}}},
		{Name: "immich", ComposeFiles: []string{"x"}, Services: []inventory.Service{
			{Name: "database", Containers: running()},
			{Name: "immich-server", Containers: running()},
			{Name: "redis", Containers: running()},
		}},
		{Name: "portainer", Services: []inventory.Service{{Name: "portainer", Containers: running()}}}, // not updatable
	}}
	rep := updates.Report{Results: []updates.Result{
		{Project: "gitea", Service: "server", Status: updates.StatusUpToDate},
		{Project: "immich", Service: "immich-server", Status: updates.StatusOutdated},
		{Project: "immich", Service: "redis", Status: updates.StatusPendingRecreate},
	}}
	a := netip.MustParseAddr
	return Data{Inventory: inv, Report: rep,
		Ports: []ports.Binding{
			{Proto: ports.TCP, IP: a("0.0.0.0"), Port: 22, Iface: "*", Process: "sshd", PID: 1},
			{Proto: ports.TCP, IP: a("0.0.0.0"), Port: 2283, Iface: "*", Ref: ports.Ref{Container: "immich_server", Project: "immich", Service: "immich-server"}},
			{Proto: ports.TCP, IP: a("::"), Port: 2283, Iface: "*", Ref: ports.Ref{Container: "immich_server", Project: "immich", Service: "immich-server"}},
			{Proto: ports.TCP, IP: a("0.0.0.0"), Port: 9000, Iface: "*", Ref: ports.Ref{Container: "portainer", Project: "portainer", Service: "portainer"}},
		},
		Declared: []ports.Declared{{Project: "gitea", Service: "server", Proto: ports.TCP, IP: a("0.0.0.0"), Port: 3000}},
		Managed:  map[string]bool{"gitea": true, "immich": true},
		Roots:    []string{"/srv/docker"},
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(t *testing.T, m Model, keys ...string) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(key(k))
		m = next.(Model)
	}
	return m, cmd
}

func TestRowsAreTree(t *testing.T) {
	m := New(data(), Funcs{})
	// gitea, server, immich, database, immich-server, redis, portainer, portainer
	if len(m.rows) != 8 {
		t.Fatalf("rows %d", len(m.rows))
	}
	if m.rows[0].service != "" || m.rows[1].service != "server" || m.rows[2].project != "immich" {
		t.Errorf("rows %+v", m.rows)
	}
}

func TestToggleServiceAndProject(t *testing.T) {
	m := New(data(), Funcs{})
	// cursor on gitea (project row) → whole project
	m, _ = press(t, m, "space")
	// move to immich-server and select it
	m, _ = press(t, m, "j", "j", "j", "j", "space")
	got := m.Targets()
	want := []updater.Target{{Project: "gitea"}, {Project: "immich", Services: []string{"immich-server"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("targets %+v", got)
	}
	// selecting every service of a project collapses to the whole project
	m, _ = press(t, m, "k", "space", "j", "j", "space")
	if got := m.Targets(); !reflect.DeepEqual(got[1], updater.Target{Project: "immich"}) {
		t.Errorf("targets %+v", got)
	}
	// project row toggle when fully selected → clears it
	m, _ = press(t, m, "k", "k", "k", "space")
	if got := m.Targets(); len(got) != 1 {
		t.Errorf("targets after clearing immich %+v", got)
	}
}

func TestNonUpdatableNotSelectable(t *testing.T) {
	m := New(data(), Funcs{})
	m, _ = press(t, m, "G", "space") // last row: portainer service
	if len(m.Targets()) != 0 {
		t.Errorf("portainer should not be selectable: %+v", m.Targets())
	}
}

func TestSelectAllOutdated(t *testing.T) {
	m := New(data(), Funcs{})
	m, _ = press(t, m, "a")
	want := []updater.Target{{Project: "immich", Services: []string{"immich-server", "redis"}}}
	if got := m.Targets(); !reflect.DeepEqual(got, want) {
		t.Errorf("targets %+v", got)
	}
}

func TestUpdateConfirmFlow(t *testing.T) {
	m := New(data(), Funcs{})
	// nothing selected → u does nothing
	m, cmd := press(t, m, "u")
	if m.mode != modeBrowse || cmd != nil {
		t.Fatal("u without selection should be a no-op")
	}
	m, _ = press(t, m, "space", "u")
	if m.mode != modeConfirm || !strings.Contains(m.View(), "gitea") {
		t.Fatalf("expected confirm screen:\n%s", m.View())
	}
	m, _ = press(t, m, "n")
	if m.mode != modeBrowse || m.Result.Targets != nil {
		t.Fatal("n should return to browse")
	}
	m, cmd = press(t, m, "u", "y")
	if m.Result.Targets == nil || cmd == nil {
		t.Fatal("y should set result and quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("expected tea.Quit")
	}
}

func TestQuit(t *testing.T) {
	m := New(data(), Funcs{})
	m, cmd := press(t, m, "q")
	if cmd == nil || m.Result.Targets != nil {
		t.Fatal("q should quit without targets")
	}
}

func TestCheckRefreshesReport(t *testing.T) {
	newRep := updates.Report{Results: []updates.Result{{Project: "gitea", Service: "server", Status: updates.StatusOutdated}}}
	m := New(data(), Funcs{Check: func() (updates.Report, error) { return newRep, nil }})
	m, cmd := press(t, m, "c")
	if !m.busy || cmd == nil {
		t.Fatal("c should start a check")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.busy || m.data.Report.Find("gitea", "server").Status != updates.StatusOutdated {
		t.Errorf("report not refreshed: %+v", m.data.Report)
	}

	m = New(data(), Funcs{Check: func() (updates.Report, error) { return updates.Report{}, errors.New("boom") }})
	m, cmd = press(t, m, "c")
	next, _ = m.Update(cmd())
	if !strings.Contains(next.(Model).View(), "boom") {
		t.Error("check error not shown")
	}
}

func TestPortsTabAndPortCheck(t *testing.T) {
	var gotSpec string
	m := New(data(), Funcs{PortCheck: func(spec string) ([]ports.Result, error) {
		gotSpec = spec
		return []ports.Result{{Proto: ports.TCP, Port: 8080, Status: ports.StatusFree}}, nil
	}})
	m, _ = press(t, m, "tab")
	if m.tab != tabPorts || !strings.Contains(m.View(), "immich-server") {
		t.Fatalf("ports tab:\n%s", m.View())
	}
	m, _ = press(t, m, "/", "8", "0", "8", "1", "backspace", "0", "enter")
	if gotSpec != "8080" {
		t.Errorf("spec %q", gotSpec)
	}
	if !strings.Contains(m.View(), "FREE") {
		t.Errorf("result not shown:\n%s", m.View())
	}
	// typing q in the input must not quit
	m, cmd := press(t, m, "/", "q")
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Error("q inside input quit the app")
		}
	}
	m, _ = press(t, m, "esc", "tab")
	if m.tab != tabUpdates {
		t.Error("tab should cycle back")
	}
}

func TestViewShowsStatuses(t *testing.T) {
	m := New(data(), Funcs{})
	v := m.View()
	for _, want := range []string{"immich", "immich-server", "outdated", "pending-recreate", "portainer"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
}

func TestPortsGroupedAndManagedByDefault(t *testing.T) {
	m := New(data(), Funcs{})
	m, _ = press(t, m, "tab")
	v := m.View()
	for _, want := range []string{"immich", "immich-server", "2283", "gitea", "stopped", "3000", "reserved", "/srv/docker"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	for _, hidden := range []string{"sshd", "portainer", "9000"} {
		if strings.Contains(v, hidden) {
			t.Errorf("%q should be filtered out by default:\n%s", hidden, v)
		}
	}
	// v4 + v6 of immich-server merged into one row
	if strings.Count(v, "2283") != 1 {
		t.Errorf("2283 should appear once:\n%s", v)
	}
}

func TestPortsFilterToggle(t *testing.T) {
	m := New(data(), Funcs{})
	m, _ = press(t, m, "tab", "f")
	v := m.View()
	for _, want := range []string{"sshd", "other", "portainer", "9000", "immich-server"} {
		if !strings.Contains(v, want) {
			t.Errorf("after f, missing %q:\n%s", want, v)
		}
	}
	m, _ = press(t, m, "f")
	if strings.Contains(m.View(), "sshd") {
		t.Error("f should toggle back to managed only")
	}
}

func TestPortsFlatView(t *testing.T) {
	m := New(data(), Funcs{})
	m, _ = press(t, m, "tab", "v")
	v := m.View()
	if !strings.Contains(v, "PROTO") || !strings.Contains(v, "immich/immich-server") {
		t.Errorf("flat view:\n%s", v)
	}
	if strings.Contains(v, "sshd") {
		t.Errorf("flat view should keep the managed filter:\n%s", v)
	}
	m, _ = press(t, m, "f")
	if !strings.Contains(m.View(), "sshd") {
		t.Errorf("flat + f should show everything:\n%s", m.View())
	}
	m, _ = press(t, m, "v")
	if strings.Contains(m.View(), "PROTO") {
		t.Error("v should switch back to grouped")
	}
}

func TestPortsScrollBounded(t *testing.T) {
	m := New(data(), Funcs{})
	m, _ = press(t, m, "tab")
	for range 50 {
		m, _ = press(t, m, "j")
	}
	if !strings.Contains(m.View(), "immich-server") && !strings.Contains(m.View(), "gitea") {
		t.Errorf("scrolled past all content:\n%s", m.View())
	}
}
