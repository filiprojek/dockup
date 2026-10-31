// Package registry resolves image tags to manifest digests on remote registries.
package registry

import (
	"context"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Digester resolves an image reference to its current remote digest.
type Digester interface {
	Digest(ctx context.Context, ref string) (string, error)
}

// Remote queries registries with a HEAD request (which doesn't count against
// Docker Hub pull limits), authenticating via ~/.docker/config.json and
// credential helpers.
type Remote struct {
	Keychain authn.Keychain
}

func (r Remote) Digest(ctx context.Context, ref string) (string, error) {
	nr, err := name.ParseReference(ref)
	if err != nil {
		return "", err
	}
	kc := r.Keychain
	if kc == nil {
		kc = authn.DefaultKeychain
	}
	desc, err := remote.Head(nr, remote.WithContext(ctx), remote.WithAuthFromKeychain(kc))
	if err != nil {
		return "", err
	}
	return desc.Digest.String(), nil
}

// Repo returns the fully-qualified repository of ref, e.g.
// "postgres:16" → "index.docker.io/library/postgres".
func Repo(ref string) (string, error) {
	nr, err := name.ParseReference(ref)
	if err != nil {
		return "", err
	}
	return nr.Context().Name(), nil
}

// LocalDigestFor picks the RepoDigests entry (from docker image inspect)
// that belongs to ref's repository.
func LocalDigestFor(ref string, repoDigests []string) (string, bool) {
	want, err := Repo(ref)
	if err != nil {
		return "", false
	}
	for _, rd := range repoDigests {
		repo, digest, ok := strings.Cut(rd, "@")
		if !ok {
			continue
		}
		if got, err := Repo(repo); err == nil && got == want {
			return digest, true
		}
	}
	return "", false
}

// IsDigestPinned reports whether ref names an immutable digest.
func IsDigestPinned(ref string) bool {
	return strings.Contains(ref, "@sha256:")
}
