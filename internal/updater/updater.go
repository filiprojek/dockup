// Package updater plans and executes docker compose updates.
package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/filiprojek/dockup/internal/inventory"
)

// Target selects a whole project (no Services) or specific services in it.
type Target struct {
	Project  string
	Services []string
}

// ParseTargets turns "proj" / "proj/svc" arguments into merged targets,
// preserving first-seen order. A bare project overrides its services.
func ParseTargets(args []string) []Target {
	var out []Target
	idx := map[string]int{}
	whole := map[string]bool{}
	for _, a := range args {
		p, s, hasSvc := strings.Cut(a, "/")
		i, seen := idx[p]
		if !seen {
			i = len(out)
			idx[p] = i
			out = append(out, Target{Project: p})
		}
		if !hasSvc {
			whole[p] = true
			out[i].Services = nil
			continue
		}
		if !whole[p] && !slices.Contains(out[i].Services, s) {
			out[i].Services = append(out[i].Services, s)
		}
	}
	return out
}

type Step struct {
	Name string // "pull", "down" or "up"
	Args []string
}

func (s Step) String() string { return strings.Join(s.Args, " ") }

type Plan struct {
	Project string
	Steps   []Step
}

// BuildPlans validates targets against the inventory and returns the compose
// commands to run. Whole projects: pull, down, up -d. Services: pull + up -d
// of just those services.
func BuildPlans(inv *inventory.Inventory, targets []Target) ([]Plan, error) {
	var plans []Plan
	for _, t := range targets {
		p := inv.Project(t.Project)
		if p == nil {
			return nil, fmt.Errorf("unknown project %q", t.Project)
		}
		if !p.Updatable() {
			return nil, fmt.Errorf("project %q has no usable compose file", t.Project)
		}
		for _, s := range t.Services {
			if p.Service(s) == nil {
				return nil, fmt.Errorf("project %q has no service %q", t.Project, s)
			}
		}
		base := []string{"docker", "compose", "--project-directory", p.Dir}
		for _, f := range p.ComposeFiles {
			base = append(base, "-f", f)
		}
		base = append(base, "-p", p.Name)
		cmd := func(args ...string) Step {
			return Step{Name: args[0], Args: append(slices.Clone(base), args...)}
		}
		plan := Plan{Project: p.Name}
		if len(t.Services) == 0 {
			plan.Steps = []Step{cmd("pull"), cmd("down"), cmd("up", "-d")}
		} else {
			plan.Steps = []Step{cmd(append([]string{"pull"}, t.Services...)...), cmd(append([]string{"up", "-d"}, t.Services...)...)}
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// Runner executes a command, streaming combined output to out.
type Runner interface {
	Run(ctx context.Context, args []string, out io.Writer) error
}

// ExecRunner runs commands on the host.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, args []string, out io.Writer) error {
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Stdout, c.Stderr, c.Stdin = out, out, os.Stdin
	return c.Run()
}

type Outcome struct {
	Project  string
	Err      error // all failed steps joined; nil on success
	Steps    []StepResult
	Duration time.Duration
}

// StepResult is one step of a plan. Ran is false when it was skipped
// because an earlier step failed.
type StepResult struct {
	Name string
	Ran  bool
	Err  error
}

type Updater struct {
	Runner Runner
	Out    io.Writer // command output and progress
	Log    io.Writer // persistent history; may be nil
	DryRun bool
	Now    func() time.Time
}

// Apply runs each plan in order; a failure affects only its own project.
// A failed pull skips the rest (nothing has changed yet). A failed down still
// runs up -d: down may already have removed some containers, and skipping up
// would leave the project down.
func (u Updater) Apply(ctx context.Context, plans []Plan) []Outcome {
	now := u.Now
	if now == nil {
		now = time.Now
	}
	logf := func(at time.Time, format string, a ...any) {
		if u.Log != nil && !u.DryRun {
			fmt.Fprintf(u.Log, "%s %s\n", at.UTC().Format(time.RFC3339), fmt.Sprintf(format, a...))
		}
	}

	var out []Outcome
	for _, p := range plans {
		start := now()
		logf(start, "%s start: %s", p.Project, stepNames(p))
		o := Outcome{Project: p.Project}
		var errs []error
		skip, downFailed := false, false
		for _, s := range p.Steps {
			r := StepResult{Name: s.Name}
			if skip {
				o.Steps = append(o.Steps, r)
				continue
			}
			fmt.Fprintf(u.Out, "==> %s\n", s)
			if u.DryRun {
				o.Steps = append(o.Steps, r)
				continue
			}
			r.Ran = true
			if err := u.Runner.Run(ctx, s.Args, u.Out); err != nil {
				r.Err = err
				errs = append(errs, fmt.Errorf("%s: %w", stepName(s, p), err))
				if s.Name == "down" {
					downFailed = true
				} else {
					skip = true
				}
			}
			o.Steps = append(o.Steps, r)
		}
		o.Err = errors.Join(errs...)
		end := now()
		o.Duration = end.Sub(start)
		if o.Err != nil {
			note := ""
			if downFailed {
				note = " (ran up -d anyway)"
			}
			logf(end, "%s FAILED after %s%s: %s", p.Project, o.Duration.Round(time.Millisecond), note, strings.ReplaceAll(o.Err.Error(), "\n", "; "))
		} else {
			logf(end, "%s ok (%s)", p.Project, o.Duration.Round(time.Millisecond))
		}
		out = append(out, o)
	}
	return out
}

// stepName strips the shared compose prefix: "pull redis", "up -d".
func stepName(s Step, p Plan) string {
	for i, a := range s.Args {
		if a == "-p" && i+2 <= len(s.Args) {
			return strings.Join(s.Args[i+2:], " ")
		}
	}
	return s.String()
}

func stepNames(p Plan) string {
	names := make([]string, len(p.Steps))
	for i, s := range p.Steps {
		names[i] = stepName(s, p)
	}
	return strings.Join(names, ", ")
}

// OpenLog opens the history log for appending, creating directories.
func OpenLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}
