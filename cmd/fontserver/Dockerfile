# fontserver's own container image -- build from the gojiberry module
# root (`docker build -f cmd/fontserver/Dockerfile .`). See
# cmd/docstore/Dockerfile's own doc comment for the shape every one of
# these four service Dockerfiles shares, and
# ../../docker-compose.yml/docs/README.md for how this fits into the
# full stack.
FROM golang:1.25-alpine AS build
# ca-certificates: fontserver's own Google Fonts proxy
# (GOJIBERRY_FONTSERVER_GOOGLE_KEY) reaches googleapis.com over TLS --
# without this the request fails cert verification.
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/fontserver ./cmd/fontserver

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/fontserver /fontserver
# No local fonts directory created here: GOJIBERRY_FONTSERVER_DIR's
# own default ("./fonts") pointing nowhere is a legitimate deployment
# (Google Fonts proxying alone, no locally-hosted fonts) -- the
# initial scan just logs a warning and continues rather than failing
# to start (fontserver/server.go's own NewServer). Mount a real
# directory at that path (or point GOJIBERRY_FONTSERVER_DIR elsewhere)
# to serve local fonts too.
EXPOSE 8082
ENTRYPOINT ["/fontserver"]
