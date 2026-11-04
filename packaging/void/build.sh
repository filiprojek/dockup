#!/bin/sh
# Builds Void Linux .xbps packages and a repository from the release
# tarballs GoReleaser left in <distdir>:
#   <outdir>/dockup-<ver>_1.<arch>.xbps, <arch>-repodata  (glibc and musl, x86_64 and aarch64)
# Needs xbps-create and xbps-rindex (Void host or a void-linux container).
# With XBPS_SIGNING_KEY_FILE set the repository and packages are signed;
# without it they are unsigned.
#
# Usage: build.sh <version> <distdir> <outdir>
#   <version> is GoReleaser's version, e.g. 0.2.0 or 0.2.0-rc1 (a leading v is stripped)
set -eu

[ $# -eq 3 ] || { echo "usage: $0 <version> <distdir> <outdir>" >&2; exit 2; }
version=${1#v}
dist=$(realpath "$2")
out=$3
key=${XBPS_SIGNING_KEY_FILE:-}
signedby=${XBPS_SIGNED_BY:-Filip Rojek <filip@filiprojek.cz>}

# xbps versions may not contain '-' or '_': 0.2.0-rc1 → 0.2.0rc1
pkgver="dockup-$(printf '%s' "$version" | tr -d -- '-_')_1"

mkdir -p "$out"
out=$(realpath "$out")
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Static binaries: the same build serves both libc variants.
for pair in amd64:x86_64 amd64:x86_64-musl arm64:aarch64 arm64:aarch64-musl; do
	goarch=${pair%%:*}
	arch=${pair#*:}
	tarball="$dist/dockup_${version}_linux_${goarch}.tar.gz"
	[ -f "$tarball" ] || { echo "missing $tarball" >&2; exit 1; }

	dest="$work/$arch"
	mkdir -p "$dest/usr/bin" "$dest/usr/share/doc/dockup" "$work/src-$arch"
	tar -xzf "$tarball" -C "$work/src-$arch"
	install -m 0755 "$work/src-$arch/dockup" "$dest/usr/bin/dockup"
	for doc in README.md config.example.yml docker-compose.example.yml; do
		install -m 0644 "$work/src-$arch/$doc" "$dest/usr/share/doc/dockup/$doc"
	done

	(
		cd "$out"
		rm -f "$pkgver.$arch.xbps" "$pkgver.$arch.xbps.sig2"
		xbps-create -q -A "$arch" -n "$pkgver" \
			-s "Status, update checks and updates for docker compose projects" \
			-H "https://github.com/filiprojek/dockup" \
			-m "Filip Rojek <filip@filiprojek.cz>" \
			"$dest"
	)
	# each arch has its own <arch>-repodata
	XBPS_TARGET_ARCH=$arch xbps-rindex -f -a "$out/$pkgver.$arch.xbps"
	if [ -n "$key" ]; then
		XBPS_TARGET_ARCH=$arch xbps-rindex --sign --signedby "$signedby" --privkey "$key" "$out"
	fi
	echo "built $pkgver.$arch.xbps"
done

if [ -n "$key" ]; then
	xbps-rindex --sign-pkg --privkey "$key" "$out"/*.xbps
fi
