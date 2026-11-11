package main

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/collab/hub"
)

func TestRenewStartupMessageHasNoCredentials(t *testing.T) {
	msg := renewStartupMessage("https://svc:hunter2@docs.example/api?token=SECRET")
	for _, s := range []string{"hunter2", "svc:", "SECRET", "token="} {
		if strings.Contains(msg, s) {
			t.Fatalf("%q leaks %q", msg, s)
		}
	}
	if !strings.Contains(msg, "https://docs.example/api") {
		t.Fatalf("%q lost the address", msg)
	}
}

func TestRemoteRenewerSendsTheGuardToken(t *testing.T) {
	r := remoteRenewer("https://docs.example", "tok")
	if r.Header.Get("X-Gojiberry-Token") != "tok" {
		t.Fatal("token header missing")
	}
	if remoteRenewer("https://docs.example", "").Header.Get("X-Gojiberry-Token") != "" {
		t.Fatal("empty token sent")
	}
}

func TestNoACLRole(t *testing.T) {
	for in, want := range map[string]hub.Role{"": hub.Owner, "owner": hub.Owner, "bogus": hub.Owner, "Editor": hub.Editor, "VIEWER": hub.Viewer, "none": ""} {
		if got := noACLRole(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestDurationEnv(t *testing.T) {
	const name = "GOJIBERRY_TEST_DURATION"
	t.Setenv(name, "")
	if _, ok, err := durationEnv(name, "x", true); ok || err != nil {
		t.Fatalf("unset: ok %v err %v", ok, err)
	}
	t.Setenv(name, "90s")
	if d, ok, err := durationEnv(name, "x", true); !ok || err != nil || d != 90*time.Second {
		t.Fatalf("set: %v %v %v", d, ok, err)
	}
	for _, bad := range []string{"soon", "0", "-5s"} {
		t.Setenv(name, bad)
		_, _, err := durationEnv(name, "a duration such as 1m", true)
		if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), bad) {
			t.Errorf("%q: error %v should name the setting and the value", bad, err)
		}
	}
	t.Setenv(name, "0")
	if _, ok, err := durationEnv(name, "x", false); !ok || err != nil {
		t.Errorf("zero allowed when not positive-only: %v %v", ok, err)
	}
}

func TestBuildHubRefusesBadSettings(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"both ways to renew":      {"GOJIBERRY_COLLAB_RENEW_URL": "https://docs.example", "GOJIBERRY_DOCSTORE_EXPIRY_TTL": "1h"},
		"bad presence interval":   {"GOJIBERRY_COLLAB_PRESENCE_INTERVAL": "often"},
		"bad renew interval":      {"GOJIBERRY_COLLAB_RENEW_INTERVAL": "0"},
		"bad expiry ttl":          {"GOJIBERRY_COLLAB_EXPIRY_TTL": "-1h"},
		"unknown storage backend": {"GOJIBERRY_DOCSTORE_BACKEND": "no-such-backend"},
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			if h, _, err := buildHub(context.Background()); err == nil {
				_ = h.Close()
				t.Fatal("accepted")
			}
		})
	}
}

func TestBuildHubAcceptsGoodSettings(t *testing.T) {
	t.Setenv("GOJIBERRY_COLLAB_PRESENCE_INTERVAL", "5s")
	t.Setenv("GOJIBERRY_COLLAB_RENEW_INTERVAL", "30s")
	t.Setenv("GOJIBERRY_COLLAB_EXPIRY_TTL", "24h")
	t.Setenv("GOJIBERRY_COLLAB_RENEW_URL", "https://svc:pw@docs.example")
	h, _, err := buildHub(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = h.Close()
}

// run serves, answers the health check, and stops on cancel.
func TestRunServesAndStopsOnCancel(t *testing.T) {
	t.Setenv("GOJIBERRY_COLLAB_ADDR", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := make(chan net.Addr, 1)
	errc := make(chan error, 1)
	go func() { errc <- run(ctx, func(a net.Addr) { addr <- a }) }()
	var a net.Addr
	select {
	case a = <-addr:
	case err := <-errc:
		t.Fatalf("run ended early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("never listened")
	}
	resp, err := http.Get("http://" + a.String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %d", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not stop")
	}
}

func TestRunFailsWhenThePortIsTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	t.Setenv("GOJIBERRY_COLLAB_ADDR", ln.Addr().String())
	if err := run(context.Background(), nil); err == nil {
		t.Fatal("served on a taken port")
	}
}
