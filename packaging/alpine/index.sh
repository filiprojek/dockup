#!/bin/sh
# Builds an apk repository from the .apk files GoReleaser left in <distdir>:
#   <outdir>/<arch>/dockup-<pkgver>.apk + APKINDEX.tar.gz, <outdir>/dockup.rsa.pub
# Run inside an Alpine container. With APK_SIGNING_KEY_FILE set the index is
# signed (and package signatures are verified); without it everything is unsigned.
#
# Usage: index.sh <distdir> <outdir>
set -eu

[ $# -eq 2 ] || { echo "usage: $0 <distdir> <outdir>" >&2; exit 2; }
dist=$1
out=$2
key=${APK_SIGNING_KEY_FILE:-}
keyname=dockup.rsa.pub # must match key_name in .goreleaser.yaml

command -v abuild-sign >/dev/null || apk add --no-cache abuild >/dev/null
command -v openssl >/dev/null || apk add --no-cache openssl >/dev/null

untrusted=--allow-untrusted
if [ -n "$key" ]; then
	key=$(realpath "$key") # used after cd below
	openssl rsa -in "$key" -pubout -out "/etc/apk/keys/$keyname" 2>/dev/null
	mkdir -p "$out"
	cp "/etc/apk/keys/$keyname" "$out/$keyname"
	untrusted=
fi

found=
for f in "$dist"/*.apk; do
	[ -e "$f" ] || continue
	found=1
	# an .apk is concatenated gzip members forming one tar stream
	info=$(gzip -dc "$f" | tar -xOf - .PKGINFO 2>/dev/null)
	name=$(printf '%s\n' "$info" | sed -n 's/^pkgname = //p')
	ver=$(printf '%s\n' "$info" | sed -n 's/^pkgver = //p')
	arch=$(printf '%s\n' "$info" | sed -n 's/^arch = //p')
	[ -n "$name" ] && [ -n "$ver" ] && [ -n "$arch" ] || { echo "bad package: $f" >&2; exit 1; }
	mkdir -p "$out/$arch"
	# apk fetches <name>-<pkgver>.apk, so the file name must be exactly that
	cp "$f" "$out/$arch/$name-$ver.apk"
done
[ -n "$found" ] || { echo "no .apk files in $dist" >&2; exit 1; }

for dir in "$out"/*/; do
	(
		cd "$dir"
		# shellcheck disable=SC2086 # $untrusted is empty or one flag
		apk index $untrusted -q -d dockup -o APKINDEX.tar.gz ./*.apk
		if [ -n "$key" ]; then
			abuild-sign -q -k "$key" -p "$keyname" APKINDEX.tar.gz
		fi
	)
	echo "indexed ${dir%/}"
done
