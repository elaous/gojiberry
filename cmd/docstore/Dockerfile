# docstore's own container image -- build from the gojiberry module
# root (`docker build -f cmd/docstore/Dockerfile .`), the same
# assumption a plain `go build ./cmd/docstore` already makes, since
# this cmd has no dependency outside its own module. See
# docs/README.md (deploy section) / ../../docker-compose.yml for how
# this fits into the full "run the whole app" stack alongside
# fontserver/syncserver/translate/server and OnDesign's own client
# image -- one Dockerfile per gojiberry binary, matching each having
# its own single-static-binary shape already (docstore.ConfigFromEnv's
# own doc comment).
FROM golang:1.26-alpine AS build
# ca-certificates isn't preinstalled on plain alpine -- installed here
# (not in the scratch runtime stage below, which has no package
# manager at all) purely so the file exists somewhere to COPY
# --from=build out of.
RUN apk add --no-cache ca-certificates
WORKDIR /src
# Dependencies first, cached separately from the source tree below --
# an ordinary edit-and-rebuild loop only re-runs `go build`, not
# `go mod download`, as long as go.mod/go.sum themselves haven't
# changed.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO_ENABLED=0: a static binary with no libc dependency, so the
# runtime stage below can be `scratch` (nothing at all, not even a
# shell) rather than needing a real base image just to satisfy a
# dynamic linker.
RUN CGO_ENABLED=0 go build -o /out/docstore ./cmd/docstore

FROM scratch
# CA certs: docstore's own S3/MongoDB backends (docstore.ConfigFromEnv)
# reach real network services over TLS; without this the connection
# fails cert verification with no other clue why, since scratch ships
# nothing else that could tell you.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/docstore /docstore
EXPOSE 8090
ENTRYPOINT ["/docstore"]
