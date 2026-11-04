#!/bin/sh
# Prepares the demo projects under /srv/docker on the isolated demo daemon.
# Runs inside the recording container (docs/demo/Dockerfile) before every tape,
# so each recording starts from the same state:
#   gitea        up to date
#   vaultwarden  outdated          (an older image re-tagged as :latest)
#   miniflux     pending-recreate  (running the old image, newer one pulled)
#   web          outdated          (target of the update recording)
#   traefik      ignored           (label dockup.ignore=true)
#   homepage     stopped           (never started → RESERVED ports)
set -eu

root=/srv/docker

project() { # project <name>: compose file on stdin
	mkdir -p "$root/$1"
	cat > "$root/$1/compose.yml"
}

# old <old-ref> <tag>: make <tag> point at an older image, so its local
# RepoDigest differs from the registry's.
old() {
	docker pull -q "$1" >/dev/null
	docker tag "$1" "$2"
}

project gitea <<'EOF'
services:
  gitea:
    image: gitea/gitea:latest
    environment:
      GITEA__database__DB_TYPE: postgres
      GITEA__database__HOST: db:5432
      GITEA__database__NAME: gitea
      GITEA__database__USER: gitea
      GITEA__database__PASSWD: gitea
    ports: ["3000:3000", "2222:22"]
    depends_on:
      db: { condition: service_healthy }
  db:
    image: postgres:17-alpine
    environment: { POSTGRES_USER: gitea, POSTGRES_PASSWORD: gitea, POSTGRES_DB: gitea }
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "gitea"]
      interval: 2s
EOF

project vaultwarden <<'EOF'
services:
  vaultwarden:
    image: vaultwarden/server:latest
    ports: ["8081:80"]
    volumes: ["data:/data"]
volumes:
  data:
EOF

project miniflux <<'EOF'
services:
  miniflux:
    image: miniflux/miniflux:latest
    environment:
      DATABASE_URL: postgres://miniflux:miniflux@db/miniflux?sslmode=disable
      RUN_MIGRATIONS: "1"
    ports: ["8082:8080"]
    depends_on:
      db: { condition: service_healthy }
  db:
    image: postgres:17-alpine
    environment: { POSTGRES_USER: miniflux, POSTGRES_PASSWORD: miniflux, POSTGRES_DB: miniflux }
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "miniflux"]
      interval: 2s
EOF

project web <<'EOF'
services:
  nginx:
    image: nginx:alpine
    ports: ["8080:80"]
EOF

project traefik <<'EOF'
services:
  traefik:
    image: traefik:v3
    command: ["--entrypoints.web.address=:80", "--entrypoints.websecure.address=:443"]
    ports: ["80:80", "443:443"]
    labels:
      dockup.ignore: "true"
EOF

project homepage <<'EOF'
services:
  homepage:
    image: ghcr.io/gethomepage/homepage:latest
    ports: ["9000:3000"]
EOF

# reset the projects whose state the recordings change
for p in vaultwarden miniflux web; do
	docker compose --progress quiet --project-directory "$root/$p" down 2>/dev/null || true
done

old vaultwarden/server:1.32.0 vaultwarden/server:latest
old nginx:1.27-alpine nginx:alpine
old miniflux/miniflux:2.2.0 miniflux/miniflux:latest

for p in gitea traefik vaultwarden web miniflux; do
	docker compose --progress quiet --project-directory "$root/$p" up -d --wait
done

# miniflux keeps running the old image while the newer one is pulled
docker pull -q miniflux/miniflux:latest >/dev/null
