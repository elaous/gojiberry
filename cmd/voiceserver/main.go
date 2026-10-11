// Command voiceserver runs the text-to-speech/speech-to-text proxy:
// GOJIBERRY_VOICESERVER_PIPER_BINARY/_PIPER_MODEL enable /speak (local
// Piper), GOJIBERRY_VOICESERVER_WHISPER_BINARY/_WHISPER_MODEL enable
// /transcribe (local whisper.cpp-compatible install). Either pair
// left empty still starts the server -- that route just reports 503
// until configured. See gojiberry/voice/server for the HTTP API.
package main

import (
	"log"
	"net/http"
	"time"

	voiceserver "github.com/elaous/gojiberry/voice/server"
)

func main() {
	cfg := voiceserver.ConfigFromEnv()
	srv := voiceserver.NewServer(cfg)
	log.Printf("voiceserver: listening on %s (piper=%q whisper=%q)", cfg.Addr, cfg.PiperBinary, cfg.WhisperBinary)
	log.Fatal((&http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}).ListenAndServe())
}
