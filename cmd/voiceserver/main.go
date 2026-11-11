// Command voiceserver runs text-to-speech and speech recognition.
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
