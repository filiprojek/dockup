# dockup

Overview, updates and ports for a server full of `docker compose` projects
(by default `/srv/docker/<project>`).

```
dockup                        # compact dashboard (uses the cached update check)
dockup status                 # projects → services: state, health, update status
dockup check [--notify]       # query registries (digest compare), refresh cache
dockup check --cached --all   # show last check, including up-to-date/skipped
dockup update immich          # whole project: pull, down, up -d
dockup update immich/redis    # single service: pull, up -d
dockup update --all --dry-run # everything with pending updates, print only
dockup ports [--proto tcp] [--project immich]
dockup ports --managed --group  # only projects under the roots, by project → service
dockup port-check 8080,9000-9010 [--ip 10.8.0.1] [--proto tcp|udp|both]
dockup port-check --free 8000-9000
dockup ui                     # interactive (also: dockup -i)
                              #   ports tab: f = all/managed, v = grouped/flat, / = port check
```

Every command accepts `--json`, `--config PATH` and `--root DIR`.

## How it works

- **Discovery**: containers are grouped by their `com.docker.compose.project`
  label, so a project's database/redis are listed with the app. Directories under
  each root with a compose file are included even when stopped. Compose files are
  parsed with compose-go (`.env` interpolation, overrides, profiles).
- **Update check**: the local image's `RepoDigests` entry is compared with the
  registry's manifest digest for the same tag (HEAD request, so it doesn't count
  against Docker Hub pull limits). `pending-recreate` means the newer image is
  already pulled but the container still runs the old one. Services that use
  `build:`, images pinned by `@sha256:`, and services marked ignored are
  skipped. Registry auth comes from `~/.docker/config.json`, including
  credential helpers.
- **Ports**: the kernel socket tables (`/proc/net/{tcp,udp}{,6}`) are merged
  with Docker's published ports, which covers `userland-proxy: false`, where
  no socket exists. Owners are resolved through `/proc/*/fd` and cgroups (for
  host-network containers).
- **port-check**: combines the kernel table, Docker-published ports, a real
  `bind()` test, and ports declared by **stopped** compose projects
  (`RESERVED`). A wildcard bind conflicts with any specific IP, and the reverse
  is also true. It exits with 1 if any port is in use or reserved.
  Privileged ports can't be bind-tested without root, so they report
  `UNKNOWN`.

Run as a user in the `docker` group. Process names for sockets owned by other
users need `sudo`. Without it, those owners show as `?`.

## Config

See [config.example.yml](config.example.yml). To opt a service out of checks,
add the label `dockup.ignore: "true"` to it.

Periodic checks with notifications (cron or a systemd timer):

```
0 */6 * * *  dockup check --notify >/dev/null
```

Update history is appended to `~/.local/state/dockup/dockup.log`.

## Install

### Alpine Linux

```
wget -O /etc/apk/keys/dockup.rsa.pub https://filiprojek.github.io/dockup/alpine/dockup.rsa.pub
echo https://filiprojek.github.io/dockup/alpine >> /etc/apk/repositories
apk update && apk add dockup
```

### Void Linux

```
echo repository=https://filiprojek.github.io/dockup/void > /etc/xbps.d/dockup.conf
xbps-install -S dockup    # accept the key signed by "Filip Rojek <filip@filiprojek.cz>"
```

Packages exist for x86_64 and aarch64, and for Void in both glibc and musl
variants. They don't depend on docker, so install the docker CLI and compose
plugin however you like. The repositories always carry the latest stable
release. Every release, pre-releases included, also has the `.apk` and `.xbps`
files attached.

### Tarball

Download a release (linux amd64 or arm64):

```
VER=0.1.0 ARCH=amd64
curl -L https://github.com/filiprojek/dockup/releases/download/v$VER/dockup_${VER}_linux_$ARCH.tar.gz | tar xz dockup
sudo install -m 0755 dockup /usr/local/bin/
dockup version
```

The image is published as `ghcr.io/filiprojek/dockup:<version>`.

## Releases

Releases are manual. Pushing to `main` only runs CI and produces a
snapshot build, which you can download under Actions → the run → Artifacts.
To publish a version:

```
git tag v0.1.0          # tags with a suffix, e.g. v0.2.0-rc1, become pre-releases
git push origin v0.1.0
```

The `release` workflow runs the tests, then GoReleaser creates the GitHub
Release (binaries, signed `.apk` packages and `checksums.txt`, with a changelog
from the commits) and pushes the images. Next it builds signed `.xbps` packages
and attaches them, signs the apk and xbps repository indexes and, for
non-pre-release tags, deploys them to GitHub Pages. To try it locally without
publishing, run `make snapshot` (output in `./dist`) and then `make packages`
(unsigned repositories in `./site`).

One-time setup for package signing:

```
openssl genrsa -out dockup.rsa 4096        # apk packages + APKINDEX
openssl genrsa -out xbps-privkey.pem 4096  # xbps repodata + packages
gh secret set APK_SIGNING_KEY < dockup.rsa
gh secret set XBPS_SIGNING_KEY < xbps-privkey.pem
```

Then set Settings → Pages → Source to "GitHub Actions". Keep a backup of both
keys outside GitHub: if a key changes, every user has to trust the new one.
The public apk key is published next to the repository, and xbps shows the
key's fingerprint on first sync.

## Build

```
make build      # ./dockup
make test       # go test -race ./...
make docker     # image; see docker-compose.example.yml for the required mounts
```
