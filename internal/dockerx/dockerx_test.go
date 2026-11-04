package dockerx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func TestFromSummary(t *testing.T) {
	s := container.Summary{
		ID:      "abc123",
		Names:   []string{"/immich_server"},
		Image:   "ghcr.io/immich-app/immich-server:release",
		ImageID: "sha256:img1",
		State:   container.StateRunning,
		Health:  &container.HealthSummary{Status: container.Unhealthy},
		Labels: map[string]string{
			LabelProject:     "immich",
			LabelService:     "immich-server",
			LabelWorkingDir:  "/srv/docker/immich",
			LabelConfigFiles: "/srv/docker/immich/docker-compose.yml,/srv/docker/immich/override.yml",
		},
		Ports: []container.PortSummary{
			{IP: netip.MustParseAddr("0.0.0.0"), PrivatePort: 2283, PublicPort: 2283, Type: "tcp"},
			{PrivatePort: 3001, Type: "tcp"}, // exposed, not published
		},
	}
	s.HostConfig.NetworkMode = "bridge"

	c := fromSummary(s)

	if c.Name != "immich_server" || c.State != "running" || c.Health != "unhealthy" {
		t.Errorf("basic fields: %+v", c)
	}
	if c.Project() != "immich" || c.Service() != "immich-server" || c.WorkingDir() != "/srv/docker/immich" {
		t.Errorf("label accessors: %q %q %q", c.Project(), c.Service(), c.WorkingDir())
	}
	if want := []string{"/srv/docker/immich/docker-compose.yml", "/srv/docker/immich/override.yml"}; !reflect.DeepEqual(c.ConfigFiles(), want) {
		t.Errorf("ConfigFiles = %v", c.ConfigFiles())
	}
	if len(c.Ports) != 2 || c.Ports[0].Public != 2283 || c.Ports[0].Proto != "tcp" {
		t.Errorf("ports %+v", c.Ports)
	}
	if !c.Running() {
		t.Error("Running() false")
	}
}

func TestFromSummaryNoHealthcheck(t *testing.T) {
	c := fromSummary(container.Summary{Names: []string{"/x"}, State: container.StateExited, Health: &container.HealthSummary{Status: container.NoHealthcheck}})
	if c.Health != "" || c.Running() {
		t.Errorf("%+v", c)
	}
	if c.ConfigFiles() != nil {
		t.Errorf("ConfigFiles without label = %v", c.ConfigFiles())
	}
}

func TestIgnoredLabel(t *testing.T) {
	for v, want := range map[string]bool{"true": true, "1": true, "yes": true, "false": false, "": false} {
		c := Container{Labels: map[string]string{LabelIgnore: v}}
		if c.Ignored() != want {
			t.Errorf("dockup.ignore=%q → %v", v, c.Ignored())
		}
	}
}

func TestEngineImageInspectNotFound(t *testing.T) {
	// what dockerd answers for an image that was never pulled
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"No such image: ghcr.io/gethomepage/homepage:latest"}`)
	}))
	t.Cleanup(srv.Close)
	c, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")), client.WithAPIVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = (&Engine{c: c}).ImageInspect(context.Background(), "ghcr.io/gethomepage/homepage:latest")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
