# translate/server's own container image -- build from the gojiberry
# module root (`docker build -f cmd/translateserver/Dockerfile .`).
# See cmd/docstore/Dockerfile's own doc comment for the shape every
# one of these four service Dockerfiles shares, and
# ../../docker-compose.yml/docs/README.md for how this fits into the
# full stack.
FROM golang:1.26-alpine AS build
# ca-certificates: with GOJIBERRY_TRANSLATESERVER_DEEPL_KEY configured,
# this proxies to DeepL's own API over TLS -- without this the request
# fails cert verification. Harmless with no key configured either
# (translate/server.NewServer's own doc comment: still starts, /translate
# just reports 503 rather than refusing to run at all).
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/translateserver ./cmd/translateserver

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/translateserver /translateserver
EXPOSE 8083
ENTRYPOINT ["/translateserver"]
