FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /dockup ./cmd/dockup

# docker:cli ships the docker CLI + compose plugin that `dockup update` drives
FROM docker:29-cli
COPY --from=build /dockup /usr/local/bin/dockup
ENTRYPOINT ["dockup"]
CMD ["--help"]
