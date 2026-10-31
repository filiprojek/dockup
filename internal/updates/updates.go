// Package updates compares local image digests against their registries.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/filiprojek/dockup/internal/dockerx"
	"github.com/filiprojek/dockup/internal/inventory"
	"github.com/filiprojek/dockup/internal/registry"
)

type Status string

const (
	StatusUpToDate        Status = "up-to-date"
	StatusOutdated        Status = "outdated"
	StatusPendingRecreate Status = "pending-recreate" // newer image pulled, container still on old one
	StatusSkipped         Status = "skipped"
	StatusError           Status = "error"
)

type Result struct {
	Project      string `json:"project"`
	Service      string `json:"service"`
	Image        string `json:"image"`
	Status       Status `json:"status"`
	Reason       string `json:"reason,omitempty"`
	LocalDigest  string `json:"local_digest,omitempty"`
	RemoteDigest string `json:"remote_digest,omitempty"`
}

// NeedsUpdate: an update action would change something.
func (r Result) NeedsUpdate() bool {
	return r.Status == StatusOutdated || r.Status == StatusPendingRecreate
}

type Report struct {
	CheckedAt time.Time `json:"checked_at"`
	Results   []Result  `json:"results"`
}

func (r Report) Find(project, service string) Result {
	for _, x := range r.Results {
		if x.Project == project && x.Service == service {
			return x
		}
	}
	return Result{}
}

func (r Report) NeedsUpdate() []Result {
	var out []Result
	for _, x := range r.Results {
		if x.NeedsUpdate() {
			out = append(out, x)
		}
	}
	return out
}

// ProjectsNeedingUpdate returns project names in report order, deduplicated.
func (r Report) ProjectsNeedingUpdate() []string {
	var out []string
	for _, x := range r.NeedsUpdate() {
		if !slices.Contains(out, x.Project) {
			out = append(out, x.Project)
		}
	}
	return out
}

func (r Report) Counts() map[Status]int {
	m := map[Status]int{}
	for _, x := range r.Results {
		m[x.Status]++
	}
	return m
}

type Checker struct {
	Docker      dockerx.Client
	Registry    registry.Digester
	Concurrency int
}

// Check evaluates every service in the inventory. Results keep inventory order.
func (c Checker) Check(ctx context.Context, inv *inventory.Inventory) Report {
	type job struct {
		idx  int
		proj string
		svc  inventory.Service
	}
	var jobs []job
	for _, p := range inv.Projects {
		for _, s := range p.Services {
			jobs = append(jobs, job{len(jobs), p.Name, s})
		}
	}

	n := max(c.Concurrency, 1)
	results := make([]Result, len(jobs))
	remote := &memo{fn: c.Registry.Digest}
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			r := c.checkService(ctx, j.svc, remote)
			r.Project, r.Service, r.Image = j.proj, j.svc.Name, j.svc.Image
			results[j.idx] = r
		}()
	}
	wg.Wait()
	return Report{CheckedAt: time.Now().UTC(), Results: results}
}

func (c Checker) checkService(ctx context.Context, s inventory.Service, remote *memo) Result {
	skip := func(reason string) Result { return Result{Status: StatusSkipped, Reason: reason} }
	switch {
	case s.Ignored:
		return skip("ignored")
	case s.Build:
		return skip("built locally")
	case s.Image == "":
		return skip("no image")
	case registry.IsDigestPinned(s.Image):
		return skip("pinned by digest")
	}

	img, err := c.Docker.ImageInspect(ctx, s.Image)
	if errors.Is(err, dockerx.ErrNotFound) {
		return skip("image not pulled")
	}
	if err != nil {
		return Result{Status: StatusError, Reason: err.Error()}
	}
	local, ok := registry.LocalDigestFor(s.Image, img.RepoDigests)
	if !ok {
		return skip("local image without registry digest")
	}

	remoteDigest, err := remote.get(ctx, s.Image)
	if err != nil {
		return Result{Status: StatusError, Reason: err.Error(), LocalDigest: local}
	}
	r := Result{LocalDigest: local, RemoteDigest: remoteDigest}
	switch {
	case remoteDigest != local:
		r.Status = StatusOutdated
	case slices.ContainsFunc(s.Containers, func(ct dockerx.Container) bool { return ct.ImageID != img.ID }):
		r.Status = StatusPendingRecreate
	default:
		r.Status = StatusUpToDate
	}
	return r
}

// memo deduplicates concurrent registry lookups of the same reference.
type memo struct {
	fn func(context.Context, string) (string, error)
	mu sync.Mutex
	m  map[string]*memoEntry
}

type memoEntry struct {
	done   chan struct{}
	digest string
	err    error
}

func (m *memo) get(ctx context.Context, ref string) (string, error) {
	m.mu.Lock()
	if m.m == nil {
		m.m = map[string]*memoEntry{}
	}
	e, ok := m.m[ref]
	if !ok {
		e = &memoEntry{done: make(chan struct{})}
		m.m[ref] = e
	}
	m.mu.Unlock()
	if !ok {
		e.digest, e.err = m.fn(ctx, ref)
		close(e.done)
	}
	<-e.done
	return e.digest, e.err
}

// Save writes the report atomically.
func Save(path string, r Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads a cached report; a missing file yields an empty report.
func Load(path string) (Report, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Report{}, nil
	}
	if err != nil {
		return Report{}, err
	}
	var r Report
	err = json.Unmarshal(data, &r)
	return r, err
}
