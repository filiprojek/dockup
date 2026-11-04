package updater

import (
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/filiprojek/dockup/internal/dockerx"
	"github.com/filiprojek/dockup/internal/inventory"
)

func svc(name string, ctrs ...dockerx.Container) inventory.Service {
	return inventory.Service{Name: name, Containers: ctrs}
}

func ctr(image, state string) dockerx.Container {
	return dockerx.Container{ImageID: image, State: state}
}

func TestBuildRecap(t *testing.T) {
	before := &inventory.Inventory{Projects: []inventory.Project{
		{Name: "immich", Services: []inventory.Service{svc("server", ctr("old", "running")), svc("redis", ctr("r1", "running"))}},
		{Name: "ha", Services: []inventory.Service{svc("ha", ctr("h1", "running")), svc("mqtt", ctr("m1", "running"))}},
		{Name: "gitea", Services: []inventory.Service{svc("gitea", ctr("g1", "running"))}},
	}}
	after := &inventory.Inventory{Projects: []inventory.Project{
		{Name: "immich", Services: []inventory.Service{svc("server", ctr("new", "running")), svc("redis", ctr("r1", "running"))}},
		{Name: "ha", Services: []inventory.Service{svc("ha", ctr("h1", "exited")), svc("mqtt", ctr("m2", "running"))}},
		{Name: "gitea", Services: []inventory.Service{svc("gitea", ctr("g1", "running"))}},
	}}
	ok := []StepResult{{Name: "pull", Ran: true}, {Name: "down", Ran: true}, {Name: "up", Ran: true}}
	downErr := errors.New("exit status 1")
	outcomes := []Outcome{
		{Project: "immich", Steps: ok, Duration: 2 * time.Second},
		{Project: "ha", Err: errors.New("down: exit status 1"), Duration: time.Minute, Steps: []StepResult{
			{Name: "pull", Ran: true}, {Name: "down", Ran: true, Err: downErr, Detail: "Error response from daemon: cannot stop container"}, {Name: "up", Ran: true}}},
		{Project: "gitea", Err: errors.New("pull: exit status 1"), Steps: []StepResult{{Name: "pull", Ran: true, Err: downErr}, {Name: "down"}, {Name: "up"}}},
	}

	r := BuildRecap(outcomes, before, after)

	want := []ProjectRecap{
		{Project: "immich", Status: RecapOK, Steps: ok, Updated: []string{"server"}, Running: 2, Total: 2, Duration: 2 * time.Second},
		{Project: "ha", Status: RecapPartial, Steps: outcomes[1].Steps, Updated: []string{"mqtt"}, Running: 1, Total: 2, Duration: time.Minute,
			Error: "down: Error response from daemon: cannot stop container"},
		{Project: "gitea", Status: RecapFailed, Steps: outcomes[2].Steps, Running: 1, Total: 1, Error: "pull: exit status 1"},
	}
	if !reflect.DeepEqual(r.Projects, want) {
		t.Errorf("got\n%+v\nwant\n%+v", r.Projects, want)
	}
	if r.Duration != time.Minute+2*time.Second {
		t.Errorf("duration %s", r.Duration)
	}
	if c := r.Counts(); c[RecapOK] != 1 || c[RecapPartial] != 1 || c[RecapFailed] != 1 || r.UpdatedServices() != 2 {
		t.Errorf("counts %v updated %d", c, r.UpdatedServices())
	}
}

// compose prints the daemon's reason as its last line; the recap shows that
// instead of "exit status 1"
func TestApplyKeepsLastOutputLineOfFailedStep(t *testing.T) {
	plans, _ := BuildPlans(testInv(), []Target{{Project: "gitea"}})
	r := &scriptRunner{out: map[string]string{
		"down": " ✔ Container a Removed\r\n\x1b[31m ✘ Container b Error\x1b[0m\nError response from daemon: cannot stop container: abc\n\n",
	}, fail: "down"}
	res := Updater{Runner: r, Out: io.Discard, Now: fixedClock()}.Apply(context.Background(), plans)

	if d := res[0].Steps[1].Detail; d != "Error response from daemon: cannot stop container: abc" {
		t.Errorf("detail %q", d)
	}
	if res[0].Steps[0].Detail != "" {
		t.Errorf("successful step has detail %q", res[0].Steps[0].Detail)
	}
}

type scriptRunner struct {
	out  map[string]string // step verb → output
	fail string
}

func (s *scriptRunner) Run(_ context.Context, args []string, out io.Writer) error {
	verb := args[slices.Index(args, "-p")+2]
	io.WriteString(out, s.out[verb])
	if verb == s.fail {
		return errors.New("exit status 1")
	}
	return nil
}
