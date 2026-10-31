BIN     ?= dockup
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint install docker clean

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

clean:
	rm -f $(BIN)
