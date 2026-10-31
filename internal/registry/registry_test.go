package registry

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// startRegistry runs an in-memory OCI registry and returns its host:port.
func startRegistry(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)
	return strings.TrimPrefix(s.URL, "http://")
}

// push uploads a random image to ref and returns its manifest digest.
func push(t *testing.T, ref string) string {
	t.Helper()
	img, err := random.Image(64, 1)
	if err != nil {
		t.Fatal(err)
	}
	r, err := name.ParseReference(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(r, img); err != nil {
		t.Fatal(err)
	}
	d, _ := img.Digest()
	return d.String()
}

func pushIndex(t *testing.T, ref string) string {
	t.Helper()
	idx, err := random.Index(64, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := name.ParseReference(ref)
	if err := remote.WriteIndex(r, idx); err != nil {
		t.Fatal(err)
	}
	d, _ := idx.Digest()
	return d.String()
}

func TestRemoteDigest(t *testing.T) {
	host := startRegistry(t)
	want := push(t, host+"/app/web:1.0")

	got, err := Remote{}.Digest(context.Background(), host+"/app/web:1.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}

	// re-push the tag → digest changes
	newer := push(t, host+"/app/web:1.0")
	got, _ = Remote{}.Digest(context.Background(), host+"/app/web:1.0")
	if got != newer || got == want {
		t.Errorf("after re-push got %s want %s", got, newer)
	}
}

func TestRemoteDigestMultiArchReturnsIndexDigest(t *testing.T) {
	host := startRegistry(t)
	want := pushIndex(t, host+"/app/multi:latest")
	got, err := Remote{}.Digest(context.Background(), host+"/app/multi") // implicit :latest
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %s want index digest %s", got, want)
	}
}

func TestRemoteDigestMissingTag(t *testing.T) {
	host := startRegistry(t)
	if _, err := (Remote{}).Digest(context.Background(), host+"/nope/nope:1"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRepo(t *testing.T) {
	cases := map[string]string{
		"postgres":                                 "index.docker.io/library/postgres",
		"postgres:16":                              "index.docker.io/library/postgres",
		"docker.io/library/postgres:16":            "index.docker.io/library/postgres",
		"valkey/valkey:8-bookworm":                 "index.docker.io/valkey/valkey",
		"ghcr.io/immich-app/immich-server:release": "ghcr.io/immich-app/immich-server",
		"ghcr.io/immich-app/immich-server@sha256:5f6a838e4e44c8e0e019d0ebfe3ee8952b69afc2809b2c25f7b0119641978e91": "ghcr.io/immich-app/immich-server",
		"localhost:5000/x:1": "localhost:5000/x",
	}
	for in, want := range cases {
		got, err := Repo(in)
		if err != nil || got != want {
			t.Errorf("Repo(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestLocalDigestFor(t *testing.T) {
	repoDigests := []string{
		"ghcr.io/other/thing@sha256:1111111111111111111111111111111111111111111111111111111111111111",
		"postgres@sha256:2222222222222222222222222222222222222222222222222222222222222222",
	}
	d, ok := LocalDigestFor("docker.io/library/postgres:16", repoDigests)
	if !ok || d != "sha256:2222222222222222222222222222222222222222222222222222222222222222" {
		t.Errorf("got %q %v", d, ok)
	}
	if _, ok := LocalDigestFor("redis:7", repoDigests); ok {
		t.Error("redis should have no local digest")
	}
}

func TestIsDigestPinned(t *testing.T) {
	if !IsDigestPinned("postgres:16@sha256:2222222222222222222222222222222222222222222222222222222222222222") {
		t.Error("tag@digest should be pinned")
	}
	if IsDigestPinned("postgres:16") {
		t.Error("tag is not pinned")
	}
}
