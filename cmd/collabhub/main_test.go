package main

import (
	"strings"
	"testing"
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
