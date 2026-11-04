# AGENTS.md

Guidance for coding agents (and humans) changing this repo. User-facing docs
are in [README.md](README.md).

## Project

`dockup` is a Go CLI + terminal UI for a server running many `docker compose`
projects under `/srv/docker/<project>`. It has four jobs:

- show the state of projects and their containers
- check for image updates by comparing registry digests
- apply updates (`compose pull`, `down`, `up -d`)
- inspect and check host ports

It is **Linux-only**, because it reads `/proc` and makes Linux socket syscalls.
The module is `github.com/filiprojek/dockup` and the entrypoint is `cmd/dockup`.

## Commands

```sh
make build                          # ./dockup with version ldflags
make test                           # go test -race ./...
make lint                           # go vet + gofmt check
make snapshot                       # local GoReleaser dry run → ./dist, publishes nothing
make packages                       # apk, xbps and apt repos from ./dist → ./site (docker)
make demo [TAPES=ui]                # re-record README images in docs/img (docker, isolated dind)
go test ./internal/ports/           # one package
go test ./internal/render/ -update  # regenerate golden files, then REVIEW the diff
```

To run against the real Docker daemon without touching your own cache or log:
`XDG_CACHE_HOME=$(mktemp -d) XDG_STATE_HOME=$(mktemp -d) ./dockup status --root <dir>`.

## Architecture

| Package | Role |
|---|---|
| `internal/config` | YAML config, defaults, XDG paths, ignore list |
| `internal/dockerx` | Narrow interface over the moby client (`Client`) plus `Fake` |
| `internal/inventory` | Groups containers by compose project; scans roots; parses compose via compose-go |
| `internal/registry` | Remote digest lookup (HEAD, docker keychain auth) and ref helpers |
| `internal/updates` | Digest comparison → `Report` (outdated / pending-recreate / skipped / error) and JSON cache |
| `internal/updater` | Builds `docker compose` plans for projects or services and runs them via `Runner`; writes the log |
| `internal/ports` | procfs socket parsing, owner resolution, `BuildTable`, `GroupBindings`, `Checker` with a real `bind()` |
| `internal/notify` | ntfy / gotify / webhook senders |
| `internal/render` | Human tables (golden-tested) and JSON |
| `internal/cli` | cobra commands; `Deps` injects every external dependency |
| `internal/tui` | bubbletea model (selection, ports tab); `Run` loops between the UI and running the update |
| `internal/version` | Build info from ldflags, falling back to `debug.ReadBuildInfo` |

Rules:

- Core packages (`inventory`, `updates`, `updater`, `ports`, ...) return
  plain structs and **never print**. Only `render`, `cli` and `tui` produce
  output.
- Every command supports `--json`. A planned web UI will reuse the core
  packages, so keep them free of terminal concerns.
- All external I/O goes through interfaces injected via `cli.Deps`:
  `dockerx.Client`, `registry.Digester`, `updater.Runner`, `ports.Binder`,
  plus the procfs paths. New I/O follows the same pattern.

## Workflow rules

- **TDD.** Write the failing test first and run it to see it fail (red),
  then implement (green), then refactor.
- **Commit after each green step.** Use small commits with Conventional
  Commit prefixes (`feat:`, `fix:`, `test:`, `docs:`, `ci:`, `build:`),
  optionally scoped, e.g. `feat(ports): …`.
- **Never push, tag or release.** Releases are manual: the maintainer pushes
  a `v*` tag.
- Keep `gofmt -l .` empty and `go vet ./...` clean before committing.

## Testing conventions

- Use fakes, not real Docker: `dockerx.Fake`, a fake `Runner`, a fake
  `Binder`, and for registries the in-memory registry
  (`github.com/google/go-containerregistry/pkg/registry`) on `httptest`.
- Fixtures:
  - procfs tables are in `internal/ports/testdata/`
  - compose projects are in `internal/inventory/testdata/srv/` and
    `internal/cli/testdata/srv/`
- Tests must not touch the real home directory. Call
  `t.Setenv("HOME", t.TempDir())` and clear the `XDG_*` variables, as
  `newEnv` does in `internal/cli/cli_test.go`.
- Golden files (`internal/render/testdata/*.golden`) are the assertion, so
  review regenerated output line by line. Never call `time.Now()` in
  rendered output; pass `now` in instead.
- `internal/ports/bind_test.go` binds real loopback sockets, which is fine.
  Never run a real `update` against the host's containers in tests.

## Gotchas

- **compose-go**:
  - `cli.WithEnvFiles()` must come before `cli.WithDotEnv`, otherwise `.env`
    is silently ignored.
  - It logs warnings through the global logrus logger. These are silenced
    in `internal/inventory/compose.go`.
  - `WithDefaultConfigPath` walks up parent directories. Use
    `findComposeFiles` instead.
- **Container IDs** are exactly 64 hex characters, and cgroup parsing
  (`ContainerIDFromCgroup`) relies on that.
- **Duplicate sockets**: `SO_REUSEPORT` and multi-threaded servers produce
  identical sockets. `BuildTable` collapses them, so keep the sort keys and
  `slices.Compact` in sync.
- **Docker without userland-proxy**: published ports have no socket, so
  `Published` entries are the source of truth (`SourceDocker`).
- **Privileged ports**: binding ports below 1024 as non-root returns
  `EACCES` → `StatusUnknown`. That's not a failure, and `port-check` exits
  with 1 only for `in-use` or `reserved`.
- **bubbletea**:
  - It queries the terminal background colour in its package `init`. In a
    pseudo-terminal that never answers (`script`), keys sent in the first
    ~5 seconds are swallowed.
  - Keys typed in one burst arrive as a single `KeyMsg`. Send them one at a
    time when smoke-testing.
  - Unit-test the model through `Update` and `View` instead.
- **Versions**: release builds set `main.version`, `main.commit` and
  `main.date` via `-ldflags` (Makefile and `.goreleaser.yaml`). Plain
  `go build` falls back to `debug.ReadBuildInfo`.

## CI / releases

- `.github/workflows/ci.yml` runs on pushes to `main` and on PRs. It does a
  gofmt check, vet and race tests, and on `main` also builds a snapshot
  artifact with GoReleaser (`--skip=publish,docker`).
- `.github/workflows/release.yml` runs **only** on `v*` tags. It runs the
  tests, then GoReleaser publishes the GitHub Release (linux amd64/arm64
  tarballs, `.apk` (signed) and `.deb` packages via `nfpms`, and checksums) and
  `ghcr.io/filiprojek/dockup` images built from `Dockerfile.release`. It then
  runs `packaging/void/build.sh` in a Void container to make signed `.xbps`
  packages (nfpm can't build xbps), attaches them to the release, and runs
  `packaging/alpine/index.sh` in an Alpine container to sign `APKINDEX` and
  `packaging/debian/repo.sh` in a Debian container to build and sign the apt
  repo (`.deb` files themselves come from `nfpms` and are unsigned; apt
  trusts them via the signed `Release`). For tags without a `-` it then
  deploys `site/` (`alpine/`, `void/`, `debian/`) to GitHub Pages.
- Signing keys come from the secrets `APK_SIGNING_KEY`, `XBPS_SIGNING_KEY`
  and `DEB_SIGNING_KEY`. Without the matching `*_SIGNING_KEY_FILE` env vars,
  the scripts and nfpm produce unsigned output, which is what snapshots and
  `make packages` use.
- apk repos fetch `<name>-<pkgver>.apk`, so `index.sh` renames packages from
  their `.PKGINFO`. xbps versions can't contain `-` or `_`, so `build.sh`
  strips them (`0.2.0-rc1` → `0.2.0rc1`).
- **Cutting a release** (maintainer only): `git tag vX.Y.Z && git push origin
  vX.Y.Z`. A `-` suffix (`v1.2.0-rc1`) makes a pre-release, which gets no
  `:latest` image and doesn't update the Pages repos. Use patch versions for
  fixes and docs, and minor versions for new features or flags.
- **One-time setup** (already done for this repo; needed again only if a key
  is lost):

  ```sh
  openssl genrsa -out dockup.rsa 4096        # apk packages + APKINDEX
  openssl genrsa -out xbps-privkey.pem 4096  # xbps repodata + packages
  gh secret set APK_SIGNING_KEY < dockup.rsa
  gh secret set XBPS_SIGNING_KEY < xbps-privkey.pem
  # apt Release file: OpenPGP key, ed25519, no passphrase, no expiry
  export GNUPGHOME=$(mktemp -d)
  gpg --batch --passphrase '' --quick-gen-key "dockup apt repository <filip@filiprojek.cz>" ed25519 sign never
  gpg --armor --export-secret-keys | gh secret set DEB_SIGNING_KEY
  ```

  Then set Settings → Pages → Source to "GitHub Actions", and under Environments →
  `github-pages` allow tags `v*` (by default only `main` may deploy). Back up
  the keys outside GitHub. A new key forces every user to re-trust it, and
  the fingerprints in README.md would change.
- After changing `.goreleaser.yaml` or `packaging/`, validate with
  `goreleaser check`, `make snapshot` and `make packages`, and keep the
  scripts `shellcheck`-clean.

## README recordings

- `docs/demo/record.sh` starts a private `docker:dind`, and the VHS
  container shares its network and pid namespaces. `setup.sh` rebuilds the
  fake projects before every tape (outdated = an older image re-tagged,
  pending-recreate = the newer image pulled after start), so tapes can be
  re-run in any order.
- Record only from this demo, never from a real daemon or `$HOME`. The
  ports views would show the host's services.
- In the TUI tape, don't toggle "all ports" (`f`): it shows VHS's own
  chromium/ttyd sockets.
- After `make demo`, look at the PNGs and a few GIF frames
  (`ffmpeg -ss <t> -i x.gif -frames:v 1 out.png`) before committing.

## Don't

- Don't add non-Linux build targets.
- Don't print from core packages or call `os.Exit` outside
  `cmd/dockup/main.go`.
- Don't commit `dist/`, `site/`, signing keys, built binaries or anything from a real `$HOME`.
- Don't add `CLAUDE.md` or `CLAUDE.local.md`. Claude Code reads this file
  only when neither exists, so instructions for all agents belong here.
