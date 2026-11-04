BIN     ?= dockup
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test lint install docker snapshot packages demo demo-clean clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/dockup

test:
	go test -race ./...

lint:
	go vet ./...
	gofmt -l . | (! grep .)

install: build
	install -m 0755 $(BIN) /usr/local/bin/$(BIN)

docker:
	docker build -t dockup:$(VERSION) .

# local dry run of a release: binaries, archives and images in ./dist, nothing published
snapshot:
	goreleaser release --snapshot --clean --skip=publish

# apk + xbps repositories in ./site from ./dist (run `make snapshot` first).
# Unsigned unless APK_SIGNING_KEY_FILE / XBPS_SIGNING_KEY_FILE point at keys.
DIST_VERSION = $(shell sed -n 's/.*"version":"\([^"]*\)".*/\1/p' dist/metadata.json)
packages:
	rm -rf site
	docker run --rm -v $(CURDIR):/w -w /w -u $$(id -u):$$(id -g) \
		$(if $(XBPS_SIGNING_KEY_FILE),-v $(abspath $(XBPS_SIGNING_KEY_FILE)):/xbps.pem:ro -e XBPS_SIGNING_KEY_FILE=/xbps.pem) \
		ghcr.io/void-linux/void-glibc-full:latest packaging/void/build.sh $(DIST_VERSION) dist site/void
	docker run --rm -v $(CURDIR):/w -w /w \
		$(if $(APK_SIGNING_KEY_FILE),-v $(abspath $(APK_SIGNING_KEY_FILE)):/dockup.rsa:ro -e APK_SIGNING_KEY_FILE=/dockup.rsa) \
		alpine:3.24 sh -c 'packaging/alpine/index.sh dist site/alpine && chown -R $(shell id -u):$(shell id -g) site'

# README screenshots/GIFs (docs/img) from docs/demo/*.tape, recorded against an
# isolated docker:dind daemon with fake projects. TAPES="ui" records just one.
demo: build
	docs/demo/record.sh $(TAPES)

demo-clean:
	docker volume rm -f dockup-demo-docker dockup-demo-run dockup-demo-srv
	docker image rm -f dockup-demo

clean:
	rm -f $(BIN)
	rm -rf dist site
