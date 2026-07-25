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
#
# No `ca-certificates` package here (unlike translateserver's own
# image, which genuinely needs it for its own outbound HTTPS call to
# DeepL) -- voiceserver makes no outbound network call of its own at
# all, only execs a local subprocess and serves plain HTTP, so there's
# no TLS handshake anywhere in this image to fail. Confirmed the hard
# way, not just reasoned about: an earlier version of this Dockerfile
# carried the same `apk add --no-cache ca-certificates` line
# translateserver's does out of copy-paste habit, which real
# integration-test coverage (OnDesign's own `integration/
# compose_test.go`, task #161) caught actually breaking the build in a
# network-restricted sandbox that couldn't reach Alpine's own package
# CDN for a package this image never used in the first place.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/voiceserver ./cmd/voiceserver

FROM alpine:3.20
COPY --from=build /out/voiceserver /voiceserver
EXPOSE 8084
ENTRYPOINT ["/voiceserver"]
