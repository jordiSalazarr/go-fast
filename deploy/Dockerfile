# The gofast history server, as a small image with no shell. Build from the
# repository's root:
#
#   docker build -f deploy/Dockerfile -t gofast-server .
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Pure Go SQLite: no cgo, a static binary.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gofast-server ./cmd/gofast-server
# The image has no shell to make /data with, so it comes from here, owned
# by distroless's nonroot user (65532), which must be able to write it.
RUN mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /gofast-server /gofast-server
COPY --from=build --chown=65532:65532 /data /data
# /data holds history.db; mount a volume there. The configuration is
# mounted at /config/server.json.
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/gofast-server", "-config", "/config/server.json"]
