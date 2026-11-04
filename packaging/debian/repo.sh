#!/bin/sh
# Builds an APT repository from the .deb files GoReleaser left in <distdir>:
#   <outdir>/pool/main/*.deb
#   <outdir>/dists/stable/main/binary-<arch>/Packages{,.gz}
#   <outdir>/dists/stable/{Release,InRelease,Release.gpg}
#   <outdir>/dockup.gpg, dockup.asc   (public key, for signed-by=)
# Run inside a Debian container. With DEB_SIGNING_KEY_FILE (an armored,
# passphrase-less OpenPGP secret key) the Release file is signed; without it
# the repository is unsigned (local dry runs only).
#
# Usage: repo.sh <distdir> <outdir>
set -eu

[ $# -eq 2 ] || { echo "usage: $0 <distdir> <outdir>" >&2; exit 2; }
dist=$1
out=$2
key=${DEB_SIGNING_KEY_FILE:-}
archs="amd64 arm64"
suite=stable

if ! command -v apt-ftparchive >/dev/null || ! command -v gpg >/dev/null; then
	apt-get update -qq >/dev/null
	DEBIAN_FRONTEND=noninteractive apt-get install -qq -y --no-install-recommends apt-utils gnupg >/dev/null
fi

[ -z "$key" ] || key=$(realpath "$key") # used after cd below

set -- "$dist"/*.deb
[ -e "$1" ] || { echo "no .deb files in $dist" >&2; exit 1; }
mkdir -p "$out/pool/main"
cp "$@" "$out/pool/main/"

cd "$out"
for arch in $archs; do
	dir="dists/$suite/main/binary-$arch"
	mkdir -p "$dir"
	apt-ftparchive --arch "$arch" packages pool > "$dir/Packages"
	gzip -9nkf "$dir/Packages"
	echo "indexed $dir ($(grep -c '^Package:' "$dir/Packages") package(s))"
done

apt-ftparchive \
	-o APT::FTPArchive::Release::Origin=dockup \
	-o APT::FTPArchive::Release::Label=dockup \
	-o APT::FTPArchive::Release::Suite="$suite" \
	-o APT::FTPArchive::Release::Codename="$suite" \
	-o APT::FTPArchive::Release::Architectures="$archs" \
	-o APT::FTPArchive::Release::Components=main \
	-o APT::FTPArchive::Release::Description="dockup packages" \
	release "dists/$suite" > Release.tmp
mv Release.tmp "dists/$suite/Release"

if [ -n "$key" ]; then
	GNUPGHOME=$(mktemp -d)
	export GNUPGHOME
	trap 'rm -rf "$GNUPGHOME"' EXIT
	gpg --batch --quiet --import "$key"
	gpg --batch --yes --quiet --clearsign -o "dists/$suite/InRelease" "dists/$suite/Release"
	gpg --batch --yes --quiet --armor --detach-sign -o "dists/$suite/Release.gpg" "dists/$suite/Release"
	gpg --batch --yes --export -o dockup.gpg
	gpg --batch --yes --armor --export -o dockup.asc
	echo "signed with $(gpg --batch --with-colons --list-keys | awk -F: '/^fpr/ {print $10; exit}')"
fi
