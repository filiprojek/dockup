package updater

import (
	"regexp"
	"strings"
	"time"

	"github.com/filiprojek/dockup/internal/inventory"
)

// RecapStatus summarises how a project's update went.
type RecapStatus string

const (
	RecapOK      RecapStatus = "ok"
	RecapPartial RecapStatus = "partial" // failed after containers were touched
	RecapFailed  RecapStatus = "failed"  // failed before anything changed (pull)
)

// ProjectRecap is one line of the end-of-run summary.
type ProjectRecap struct {
	Project  string        `json:"project"`
	Status   RecapStatus   `json:"status"`
	Steps    []StepResult  `json:"steps"`
	Updated  []string      `json:"updated,omitempty"` // services now running a different image
	Running  int           `json:"running"`           // services running after the update
	Total    int           `json:"total"`
	Duration time.Duration `json:"duration"`
	Error    string        `json:"error,omitempty"`
}

// Recap is the end-of-run summary of an update, like Ansible's PLAY RECAP.
type Recap struct {
	Projects []ProjectRecap `json:"projects"`
	Duration time.Duration  `json:"duration"`
}

// BuildRecap combines the outcomes with the inventory before and after the
// update to tell which services changed image and what is running now.
func BuildRecap(outcomes []Outcome, before, after *inventory.Inventory) Recap {
	var r Recap
	for _, o := range outcomes {
		pr := ProjectRecap{Project: o.Project, Status: RecapOK, Steps: o.Steps, Duration: o.Duration}
		r.Duration += o.Duration
		if o.Err != nil {
			pr.Status = RecapPartial
			if len(o.Steps) > 0 && o.Steps[0].Err != nil {
				pr.Status = RecapFailed
			}
			for _, s := range o.Steps {
				if s.Err != nil {
					msg := s.Detail
					if msg == "" {
						// compose wrote the reason straight to the terminal
						msg = s.Err.Error() + " (see output above)"
					}
					pr.Error = s.Name + ": " + msg
					break
				}
			}
		}
		if p := after.Project(o.Project); p != nil {
			var old *inventory.Project
			if before != nil {
				old = before.Project(o.Project)
			}
			for _, s := range p.Services {
				pr.Total++
				if s.State() == inventory.StateRunning {
					pr.Running++
				}
				if old != nil && imageChanged(old.Service(s.Name), s) {
					pr.Updated = append(pr.Updated, s.Name)
				}
			}
		}
		r.Projects = append(r.Projects, pr)
	}
	return r
}

func imageChanged(old *inventory.Service, now inventory.Service) bool {
	if old == nil || len(old.Containers) == 0 || len(now.Containers) == 0 {
		return false
	}
	ids := map[string]bool{}
	for _, c := range old.Containers {
		ids[c.ImageID] = true
	}
	for _, c := range now.Containers {
		if !ids[c.ImageID] {
			return true
		}
	}
	return false
}

// Counts returns the number of projects per status.
func (r Recap) Counts() map[RecapStatus]int {
	m := map[RecapStatus]int{}
	for _, p := range r.Projects {
		m[p.Status]++
	}
	return m
}

// UpdatedServices is the total number of services that changed image.
func (r Recap) UpdatedServices() int {
	n := 0
	for _, p := range r.Projects {
		n += len(p.Updated)
	}
	return n
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// lastLine remembers the last non-empty output line, without ANSI codes
// and progress redraws (\r).
type lastLine struct{ buf, last string }

func (l *lastLine) Write(p []byte) (int, error) {
	l.buf += string(p)
	for {
		i := strings.IndexAny(l.buf, "\r\n")
		if i < 0 {
			break
		}
		l.take(l.buf[:i])
		l.buf = l.buf[i+1:]
	}
	return len(p), nil
}

func (l *lastLine) take(s string) {
	if s = strings.TrimSpace(ansi.ReplaceAllString(s, "")); s != "" {
		l.last = s
	}
}

func (l *lastLine) String() string {
	l.take(l.buf)
	l.buf = ""
	return l.last
}
