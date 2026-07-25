# voiceserver's own container image -- build from the gojiberry
# module root (`docker build -f cmd/voiceserver/Dockerfile .`).
#
# Unlike cmd/translateserver/Dockerfile (FROM scratch: it only ever
# makes outbound HTTPS calls to DeepL), this one execs a Piper/whisper
# binary as a subprocess per request (voiceserver/server.go's own
# execPiper/execWhisper), so the base image needs a real shell/libc
# for those to run against, plus the binaries themselves and their
# model files -- none of which this Dockerfile bundles, since which
# voice/model an operator wants is very much their own choice, the
# same "still runs, feature reports unavailable until configured"
# posture translateserver takes with no DeepL key. Point
# GOJIBERRY_VOICESERVER_PIPER_BINARY/_WHISPER_BINARY at paths inside a
# volume you mount in (or extend this image with your own COPY/RUN
# steps to install them directly) -- see docs/README.md's own "Voice"
# section for the full setup.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/voiceserver ./cmd/voiceserver

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/voiceserver /voiceserver
EXPOSE 8084
ENTRYPOINT ["/voiceserver"]
