// Package inventory discovers compose projects and their containers and
// groups them for display, updates and port checks.
package inventory

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/filiprojek/dockup/internal/dockerx"
	"github.com/filiprojek/dockup/internal/ports"
)

type State string

const (
	StateRunning State = "running"
	StatePartial State = "partial" // some replicas running
	StateStopped State = "stopped"
	StateMissing State = "missing" // declared in compose, no container
)

type Service struct {
	Name       string              `json:"name"`
	Image      string              `json:"image"`
	Build      bool                `json:"build,omitempty"`
	Ignored    bool                `json:"ignored,omitempty"`
	Orphan     bool                `json:"orphan,omitempty"` // container exists but not in compose file
	Containers []dockerx.Container `json:"containers"`
	Declared   []ports.Declared    `json:"declared_ports,omitempty"`
}

func (s Service) State() State {
	if len(s.Containers) == 0 {
		return StateMissing
	}
	n := 0
	for _, c := range s.Containers {
		if c.Running() {
			n++
		}
	}
	switch n {
	case 0:
		return StateStopped
	case len(s.Containers):
		return StateRunning
	}
	return StatePartial
}

func (s Service) Unhealthy() bool {
	for _, c := range s.Containers {
		if c.Health == "unhealthy" {
			return true
		}
	}
	return false
}

type Project struct {
	Name         string    `json:"name"`
	Dir          string    `json:"dir,omitempty"`
	ComposeFiles []string  `json:"compose_files,omitempty"`
	Standalone   bool      `json:"standalone,omitempty"` // a container not managed by compose
	LoadErr      string    `json:"load_error,omitempty"`
	Services     []Service `json:"services"`
}

// Updatable: dockup can only drive compose for projects with a parsed compose file.
func (p *Project) Updatable() bool {
	return !p.Standalone && len(p.ComposeFiles) > 0 && p.LoadErr == ""
}

func (p *Project) Running() bool {
	for _, s := range p.Services {
		if st := s.State(); st == StateRunning || st == StatePartial {
			return true
		}
	}
	return false
}

func (p *Project) Service(name string) *Service {
	for i := range p.Services {
		if p.Services[i].Name == name {
			return &p.Services[i]
		}
	}
	return nil
}

type Inventory struct {
	Projects []Project `json:"projects"`
}

func (inv *Inventory) Project(name string) *Project {
	for i := range inv.Projects {
		if inv.Projects[i].Name == name {
			return &inv.Projects[i]
		}
	}
	return nil
}

type Summary struct {
	Projects        int `json:"projects"`
	ProjectsRunning int `json:"projects_running"`
	Containers      int `json:"containers"`
	Running         int `json:"running"`
	Stopped         int `json:"stopped"`
	Unhealthy       int `json:"unhealthy"`
}

func (inv *Inventory) Summary() Summary {
	var s Summary
	for i := range inv.Projects {
		p := &inv.Projects[i]
		s.Projects++
		if p.Running() {
			s.ProjectsRunning++
		}
		for _, svc := range p.Services {
			for _, c := range svc.Containers {
				s.Containers++
				if c.Running() {
					s.Running++
				} else {
					s.Stopped++
				}
				if c.Health == "unhealthy" {
					s.Unhealthy++
				}
			}
		}
	}
	return s
}

// DeclaredStopped returns compose-declared host ports of projects that aren't
// running (running ones already appear as real sockets).
func (inv *Inventory) DeclaredStopped() []ports.Declared {
	var out []ports.Declared
	for i := range inv.Projects {
		p := &inv.Projects[i]
		if p.Running() {
			continue
		}
		for _, s := range p.Services {
			out = append(out, s.Declared...)
		}
	}
	return out
}

// Managed returns the projects whose directory lies inside one of roots,
// i.e. the ones the user maintains (e.g. under /srv/docker).
func (inv *Inventory) Managed(roots []string) map[string]bool {
	out := map[string]bool{}
	for _, p := range inv.Projects {
		if p.Standalone || p.Dir == "" {
			continue
		}
		for _, r := range roots {
			rel, err := filepath.Rel(r, p.Dir)
			if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") {
				out[p.Name] = true
				break
			}
		}
	}
	return out
}

// Builder assembles an Inventory from Docker and compose roots.
type Builder struct {
	Docker dockerx.Client
	Roots  []string
	// Ignore reports config-level exclusions; may be nil.
	Ignore func(project, service string) bool
}

func (b Builder) Build(ctx context.Context) (*Inventory, error) {
	containers, err := b.Docker.Containers(ctx)
	if err != nil {
		return nil, err
	}

	byName := map[string]*Project{}
	var order []*Project
	add := func(p *Project) *Project {
		if existing, ok := byName[p.Name]; ok {
			return existing
		}
		byName[p.Name] = p
		order = append(order, p)
		return p
	}

	// 1. compose projects found on disk
	for _, root := range b.Roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root, e.Name())
			files := findComposeFiles(dir)
			if files == nil {
				continue
			}
			add(b.projectFromCompose(ctx, dir, files, "", e.Name()))
		}
	}

	// 2. projects only known from container labels
	for _, c := range containers {
		name := c.Project()
		if name == "" || byName[name] != nil {
			continue
		}
		dir, files := c.WorkingDir(), existing(c.ConfigFiles())
		if files == nil && dir != "" {
			files = findComposeFiles(dir)
		}
		if files != nil {
			add(b.projectFromCompose(ctx, dir, files, name, name))
		} else {
			add(&Project{Name: name, Dir: dir})
		}
	}

	// 3. attach containers
	for _, c := range containers {
		if c.Project() == "" {
			p := add(&Project{Name: c.Name, Standalone: true})
			p.Services = append(p.Services, Service{Name: c.Name, Image: c.Image})
			attach(p, c.Name, c)
			continue
		}
		attach(byName[c.Project()], c.Service(), c)
	}

	inv := &Inventory{}
	for _, p := range order {
		for i := range p.Services {
			s := &p.Services[i]
			if b.Ignore != nil && b.Ignore(p.Name, s.Name) {
				s.Ignored = true
			}
			for _, c := range s.Containers {
				if c.Ignored() {
					s.Ignored = true
				}
			}
		}
		slices.SortFunc(p.Services, func(a, b Service) int { return cmp.Compare(a.Name, b.Name) })
		inv.Projects = append(inv.Projects, *p)
	}
	slices.SortFunc(inv.Projects, func(a, b Project) int { return cmp.Compare(a.Name, b.Name) })
	return inv, nil
}

func attach(p *Project, service string, c dockerx.Container) {
	s := p.Service(service)
	if s == nil {
		p.Services = append(p.Services, Service{Name: service, Image: c.Image, Orphan: !p.Standalone && len(p.ComposeFiles) > 0})
		s = &p.Services[len(p.Services)-1]
	}
	s.Containers = append(s.Containers, c)
}

func (b Builder) projectFromCompose(ctx context.Context, dir string, files []string, name, fallback string) *Project {
	cp, err := loadCompose(ctx, dir, files, name)
	if err != nil {
		return &Project{Name: fallback, Dir: dir, ComposeFiles: files, LoadErr: err.Error()}
	}
	p := &Project{Name: cp.Name, Dir: dir, ComposeFiles: files}
	add := func(svcs types.Services) {
		for _, s := range svcs {
			p.Services = append(p.Services, Service{
				Name:     s.Name,
				Image:    s.Image,
				Build:    s.Build != nil,
				Ignored:  dockerx.Truthy(s.Labels[dockerx.LabelIgnore]),
				Declared: declaredPorts(cp.Name, s.Name, s.Ports),
			})
		}
	}
	add(cp.Services)
	add(cp.DisabledServices)
	return p
}

func existing(paths []string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}
