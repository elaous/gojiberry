# brailleserver's own container image -- build from the gojiberry
# module root (`docker build -f cmd/brailleserver/Dockerfile .`).
#
# Same shape as cmd/voiceserver/Dockerfile: this execs a lou_translate
# binary as a subprocess per request (brailleserver/server.go's own
# execLouTranslate), so the base image needs a real shell/libc for
# that to run against, plus liblouis itself and its translation
# tables -- none of which this Dockerfile bundles, since which
# liblouis build/tables an operator wants is their own choice, the
# same "still runs, feature reports unavailable until configured"
# posture voiceserver takes with no Piper/whisper binary. Point
# GOJIBERRY_BRAILLESERVER_LOU_TRANSLATE_BINARY at a path inside a
# volume you mount in (or extend this image with your own COPY/RUN
# steps installing liblouis directly) -- see docs/README.md's own
# "Braille" section for the full setup.
#
# No `ca-certificates` here for the same reason voiceserver's own
# Dockerfile carries none: this image makes no outbound network call
# of its own at all, only execs a local subprocess and serves plain
# HTTP.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/brailleserver ./cmd/brailleserver

FROM alpine:3.20
COPY --from=build /out/brailleserver /brailleserver
EXPOSE 8085
ENTRYPOINT ["/brailleserver"]
