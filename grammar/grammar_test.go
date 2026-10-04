package grammar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestParseIssuesToleratesNoiseAndBadRanges(t *testing.T) {
	text := "He go to school. They was late."
	answer := "Sure! ```json\n[{\"offset\":3,\"length\":2,\"message\":\"agreement\",\"suggestion\":\"goes\"}," +
		"{\"offset\":99,\"length\":3,\"message\":\"outside\"},{\"offset\":-1,\"length\":2},{\"offset\":0,\"length\":0}," +
		"{\"offset\":0,\"length\":4,\"text\":\"They\",\"message\":\"wrong place, right words\",\"suggestion\":\"x\"}," +
		"{\"offset\":0,\"length\":4,\"text\":\"nowhere\",\"message\":\"words not in the text\"}]\n```"
	got, err := ParseIssues(answer, text)
	if err != nil {
		t.Fatal(err)
	}
	want := []Issue{
		{From: 3, To: 5, Message: "agreement", Suggestion: "goes"},
		{From: 17, To: 21, Message: "wrong place, right words", Suggestion: "x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if _, err := ParseIssues("no array here", text); err == nil {
		t.Error("expected an error")
	}
	if _, err := ParseIssues("[{broken", text); err == nil {
		t.Error("expected an error for broken JSON")
	}
}

func TestCheckAsksTheModel(t *testing.T) {
	var prompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model, Prompt, Format string
			Stream                bool
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		prompt = req.Prompt
		if r.URL.Path != "/api/generate" || req.Model != "m1" || req.Format != "json" || req.Stream {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"response": `[{"offset":3,"length":2,"message":"agreement","suggestion":"goes"}]`})
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL + "/", Model: "m1"}
	got, err := c.Check(context.Background(), "He go to school.")
	if err != nil || len(got) != 1 || got[0].Suggestion != "goes" || got[0].From != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	if !strings.HasSuffix(prompt, "He go to school.") {
		t.Errorf("the text is at the end of the prompt: %q", prompt)
	}
	if got, err := c.Check(context.Background(), " "); err != nil || got != nil {
		t.Errorf("blank text: %v %v", got, err)
	}
	if _, err := (Client{BaseURL: srv.URL}).Check(context.Background(), "x y"); err == nil {
		t.Error("no model, no check")
	}
	if _, err := (Client{BaseURL: srv.URL, Model: "other"}).Check(context.Background(), "x y"); err == nil {
		t.Error("a refusing server is an error")
	}
}
