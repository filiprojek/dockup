// Package dockerx is a narrow, fakeable view of the Docker Engine API.
package dockerx

import (
	"context"
	"errors"
	"net/netip"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const (
	LabelProject     = "com.docker.compose.project"
	LabelService     = "com.docker.compose.service"
	LabelWorkingDir  = "com.docker.compose.project.working_dir"
	LabelConfigFiles = "com.docker.compose.project.config_files"
	LabelIgnore      = "dockup.ignore"
)

type Port struct {
	IP      netip.Addr `json:"ip"`
	Private uint16     `json:"private"`
	Public  uint16     `json:"public,omitempty"`
	Proto   string     `json:"proto"`
}

type Container struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	ImageID     string            `json:"image_id"`
	State       string            `json:"state"`
	Health      string            `json:"health,omitempty"`
	NetworkMode string            `json:"network_mode,omitempty"`
	Labels      map[string]string `json:"-"`
	Ports       []Port            `json:"ports,omitempty"`
}

func (c Container) Project() string    { return c.Labels[LabelProject] }
func (c Container) Service() string    { return c.Labels[LabelService] }
func (c Container) WorkingDir() string { return c.Labels[LabelWorkingDir] }
func (c Container) Running() bool      { return c.State == string(container.StateRunning) }

func (c Container) ConfigFiles() []string {
	v := c.Labels[LabelConfigFiles]
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

// Ignored reports whether the container opted out via dockup.ignore.
func (c Container) Ignored() bool { return Truthy(c.Labels[LabelIgnore]) }

// Truthy parses label boolean values ("true", "1", "yes").
func Truthy(s string) bool {
	switch strings.ToLower(s) {
	case "true", "1", "yes":
		return true
	}
	return false
}

type Image struct {
	ID          string   `json:"id"`
	RepoDigests []string `json:"repo_digests"`
}

// Client is the subset of Docker that dockup needs.
type Client interface {
	Containers(ctx context.Context) ([]Container, error)
	ImageInspect(ctx context.Context, ref string) (Image, error)
}

// Engine adapts the moby client to Client.
type Engine struct{ c *client.Client }

// NewEngine connects using DOCKER_HOST etc. with API version negotiation.
func NewEngine() (*Engine, error) {
	c, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return &Engine{c: c}, nil
}

func (e *Engine) Containers(ctx context.Context) ([]Container, error) {
	res, err := e.c.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(res.Items))
	for _, s := range res.Items {
		out = append(out, fromSummary(s))
	}
	return out, nil
}

func (e *Engine) ImageInspect(ctx context.Context, ref string) (Image, error) {
	res, err := e.c.ImageInspect(ctx, ref)
	if err != nil {
		return Image{}, err
	}
	return Image{ID: res.ID, RepoDigests: res.RepoDigests}, nil
}

func fromSummary(s container.Summary) Container {
	c := Container{
		ID:          s.ID,
		Image:       s.Image,
		ImageID:     s.ImageID,
		State:       string(s.State),
		NetworkMode: s.HostConfig.NetworkMode,
		Labels:      s.Labels,
	}
	if len(s.Names) > 0 {
		c.Name = strings.TrimPrefix(s.Names[0], "/")
	}
	if s.Health != nil && s.Health.Status != container.NoHealthcheck {
		c.Health = string(s.Health.Status)
	}
	for _, p := range s.Ports {
		c.Ports = append(c.Ports, Port{IP: p.IP.Unmap(), Private: p.PrivatePort, Public: p.PublicPort, Proto: p.Type})
	}
	return c
}

// Fake is an in-memory Client for tests.
type Fake struct {
	List   []Container
	Images map[string]Image
	Err    error
}

func (f *Fake) Containers(context.Context) ([]Container, error) { return f.List, f.Err }

func (f *Fake) ImageInspect(_ context.Context, ref string) (Image, error) {
	if img, ok := f.Images[ref]; ok {
		return img, nil
	}
	return Image{}, ErrNotFound
}

var ErrNotFound = errors.New("image not found")
