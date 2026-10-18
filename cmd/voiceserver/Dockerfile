# voiceserver's own container image -- build from the gojiberry module root:
#
#   docker build -f cmd/voiceserver/Dockerfile -t voiceserver .
#
# It carries Piper (the `piper-tts` Python package, which brings its own espeak-ng data) so that /speak works, and no
# voices: which voices to offer, and under what licence, is the operator's choice. Mount a folder with the voices
# (each an .onnx and its .onnx.json) and a voices.json that lists them at /voices:
#
#   docker run -p 8084:8084 -v $PWD/voices:/voices:ro \
#     -e GOJIBERRY_VOICESERVER_ALLOW_ORIGINS=https://your-page.example voiceserver
#
# voices.json is [{"id", "name", "lang", "licence", "model"}] with "model" the path INSIDE the container
# (/voices/en_US-lessac-medium.onnx); docs/reader-voices.md has an example. With no voices.json /speak uses
# GOJIBERRY_VOICESERVER_PIPER_MODEL, if set. The image has no whisper, so /transcribe answers 503 until the operator
# adds one (the same "still runs, feature reports unavailable until configured" posture as before).
#
# voiceserver makes no outbound network call of its own: it execs Piper for each request and serves plain HTTP (put TLS
# in front of it). The runtime image is Debian because Piper's wheels are built for glibc.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/voiceserver ./cmd/voiceserver

FROM python:3.11-slim
# the Piper version is pinned: a new release is tried and then changed here
ARG PIPER_TTS_VERSION=1.8.0
RUN python -m venv /opt/piper && /opt/piper/bin/pip install --no-cache-dir "piper-tts==${PIPER_TTS_VERSION}" \
 && useradd --system --no-create-home --uid 10001 voiceserver && mkdir /voices
COPY --from=build /out/voiceserver /usr/local/bin/voiceserver
ENV GOJIBERRY_VOICESERVER_ADDR=:8084 \
    GOJIBERRY_VOICESERVER_PIPER_BINARY=/opt/piper/bin/piper \
    GOJIBERRY_VOICESERVER_VOICES=/voices/voices.json
VOLUME /voices
USER voiceserver
EXPOSE 8084
HEALTHCHECK --interval=30s --timeout=3s CMD ["python", "-c", "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:8084/health', timeout=2).status == 200 else 1)"]
ENTRYPOINT ["/usr/local/bin/voiceserver"]
