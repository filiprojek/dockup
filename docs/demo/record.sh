#!/bin/sh
# Records the README screenshots/GIFs (docs/img/) from docs/demo/*.tape against
# an isolated docker:dind daemon with fake projects. Nothing from the host's
# Docker shows up. Usage: record.sh [tape...]   (default: all, update last)
set -eu
cd "$(dirname "$0")/../.."

tapes=${*:-status ports ui update}
dind=dockup-demo-dind

docker build -q -t dockup-demo -f docs/demo/Dockerfile . >/dev/null
docker rm -f "$dind" >/dev/null 2>&1 || true
docker run -d --privileged --name "$dind" \
	-v dockup-demo-docker:/var/lib/docker -v dockup-demo-run:/var/run \
	docker:dind dockerd -H unix:///var/run/docker.sock >/dev/null

run() {
	docker run --rm --network "container:$dind" --pid "container:$dind" \
		-v dockup-demo-run:/var/run -v dockup-demo-srv:/srv/docker \
		-v "$PWD/docs:/src/docs" -w /src "$@"
}

cleanup() {
	# files written by the container are root-owned
	run --entrypoint chown dockup-demo -R "$(id -u):$(id -g)" docs/img || true
	docker rm -f "$dind" >/dev/null
}
trap cleanup EXIT

run --entrypoint sh dockup-demo -c 'until docker info >/dev/null 2>&1; do sleep 1; done'
for t in $tapes; do
	echo "recording $t"
	run --entrypoint sh dockup-demo -c "demo-setup && vhs -q docs/demo/$t.tape"
done
