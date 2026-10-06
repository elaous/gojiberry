# syncserver's own container image -- build from the gojiberry module
# root (`docker build -f cmd/syncserver/Dockerfile .`). See
# cmd/docstore/Dockerfile's own doc comment for the shape every one of
# these four service Dockerfiles shares, and
# ../../docker-compose.yml/docs/README.md for how this fits into the
# full stack. No ca-certificates layer here, unlike docstore/
# fontserver/translate/server: the delta-sync relay never makes an
# outbound network call of its own, just relays what clients POST to
# it (syncserver.Server's own doc comment).
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/syncserver ./cmd/syncserver

FROM scratch
COPY --from=build /out/syncserver /syncserver
EXPOSE 8080
ENTRYPOINT ["/syncserver"]
