# Recording environment for the README demo (see `make demo`).
# VHS + docker CLI + the locally built ./dockup. Talks to an isolated
# docker:dind daemon, never to the host's.
FROM docker:cli AS docker
FROM ghcr.io/charmbracelet/vhs:latest
COPY --from=docker /usr/local/bin/docker /usr/local/bin/docker
COPY --from=docker /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose
COPY dockup /usr/local/bin/dockup
COPY docs/demo/setup.sh /usr/local/bin/demo-setup
ENV HOME=/home/demo XDG_CACHE_HOME=/home/demo/.cache XDG_STATE_HOME=/home/demo/.local/state
RUN mkdir -p /home/demo /srv/docker
WORKDIR /home/demo
