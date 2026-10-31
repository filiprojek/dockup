BIN     ?= dockup
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test lint install docker snapshot clean

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

clean:
	rm -f $(BIN)
	rm -rf dist
