// Command voiceserver runs the text-to-speech/speech-to-text proxy:
// GOJIBERRY_VOICESERVER_PIPER_BINARY/_PIPER_MODEL enable /speak (local
// Piper), GOJIBERRY_VOICESERVER_WHISPER_BINARY/_WHISPER_MODEL enable
// /transcribe (local whisper.cpp-compatible install). Either pair
// left empty still starts the server -- that route just reports 503
// until configured. See gojiberry/voice/server for the HTTP API.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/elaous/gojiberry/observe"
	voiceserver "github.com/elaous/gojiberry/voice/server"
)

func main() {
	cfg := voiceserver.ConfigFromEnv()
	srv := voiceserver.NewServer(cfg)
	log.Printf("voiceserver: listening on %s (piper=%q whisper=%q)", cfg.Addr, cfg.PiperBinary, cfg.WhisperBinary)
	obs, err := observe.Setup(context.Background(), os.Getenv, "gojiberry-voiceserver")
	if err != nil {
		log.Fatalf("voiceserver: %v", err)
	}
	log.Fatal(observe.Serve(&http.Server{Addr: cfg.Addr, Handler: observe.Middleware(obs, srv.Handler()), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}, obs))
}
