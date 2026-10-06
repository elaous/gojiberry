package translateserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDeeplClientSendsTheDocumentedRequest(t *testing.T) {
	var got struct {
		auth, ctype, path, method string
		body                      map[string]any
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.auth, got.ctype, got.path, got.method = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.URL.Path, r.Method
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got.body)
		_, _ = io.WriteString(w, `{"translations":[{"detected_source_language":"EN","text":"Bonjour"},{"detected_source_language":"EN","text":"Monde"}]}`)
	}))
	defer ts.Close()
	c, err := newDeeplClient("secret")
	if err != nil {
		t.Fatal(err)
	}
	c.base = ts.URL
	res, err := c.Translate(context.Background(), []string{"Hello", "World"}, "FR", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/v2/translate" || got.auth != "DeepL-Auth-Key secret" || got.ctype != "application/json" {
		t.Errorf("%+v", got)
	}
	if got.body["target_lang"] != "FR" || got.body["source_lang"] != nil || len(got.body["text"].([]any)) != 2 {
		t.Errorf("body %v (source_lang is left out when empty)", got.body)
	}
	if len(res) != 2 || res[0] != (Translation{Text: "Bonjour", DetectedSourceLanguage: "EN"}) || res[1].Text != "Monde" {
		t.Errorf("%+v", res)
	}
	if _, err := c.Translate(context.Background(), []string{"a"}, "FR", "EN"); err != nil || got.body["source_lang"] != "EN" {
		t.Errorf("source language is sent: %v %v", err, got.body)
	}
}

func TestDeeplClientErrorsAndEndpoints(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "bad") {
			_, _ = io.WriteString(w, `not json`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"Wrong key"}`)
	}))
	defer ts.Close()
	c, _ := newDeeplClient("k")
	c.base = ts.URL
	if _, err := c.Translate(context.Background(), []string{"a"}, "FR", ""); err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "Wrong key") {
		t.Errorf("%v", err)
	}
	c.base = ts.URL + "?bad"
	if _, err := c.Translate(context.Background(), []string{"a"}, "FR", ""); err == nil {
		t.Error("an unreadable reply is an error")
	}
	if _, err := newDeeplClient(" "); err == nil {
		t.Error("an empty key is refused")
	}
	if free, _ := newDeeplClient("abc:fx"); free.base != deeplFreeBase {
		t.Errorf("free keys use the free endpoint: %s", free.base)
	}
	if pro, _ := newDeeplClient("abc"); pro.base != deeplProBase {
		t.Errorf("%s", pro.base)
	}
}

func TestDeeplClientEmptyReplyLongErrorAndCancellation(t *testing.T) {
	mode := "empty"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "empty":
			_, _ = io.WriteString(w, `{"translations":[]}`)
		case "long":
			w.WriteHeader(456)
			_, _ = io.WriteString(w, strings.Repeat("x", 5000))
		case "slow":
			_, _ = io.Copy(io.Discard, r.Body) // reading the body lets the server notice the client leaving
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
		}
	}))
	defer ts.Close()
	c, _ := newDeeplClient("k")
	c.base = ts.URL
	if res, err := c.Translate(context.Background(), []string{"a"}, "FR", ""); err != nil || len(res) != 0 {
		t.Errorf("an empty list is returned as is: %v %v", res, err)
	}
	mode = "long"
	if _, err := c.Translate(context.Background(), []string{"a"}, "FR", ""); err == nil || len(err.Error()) > 400 || !strings.Contains(err.Error(), "456") {
		t.Errorf("a long error body is cut: %v", err)
	}
	mode = "slow"
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Translate(ctx, []string{"a"}, "FR", ""); err == nil {
		t.Error("a cancelled context stops the call")
	}
}
