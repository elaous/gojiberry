package mentionmail

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/mail"
)

type fake struct {
	sent []mail.Message
	fail map[string]bool
}

func (f *fake) Send(_ context.Context, m mail.Message) error {
	if f.fail[m.To[0]] {
		return errors.New("refused")
	}
	f.sent = append(f.sent, m)
	return nil
}

func commented(t *testing.T, texts ...string) *canvas.CommentSet {
	t.Helper()
	s := canvas.NewCommentSet()
	for i, x := range texts {
		s.Add(canvas.Comment{ID: string(rune('a' + i)), Author: "bo", Text: x})
	}
	return s
}

func TestNotifyMailsEachPersonOnceWithWhatIsNew(t *testing.T) {
	s := commented(t, "@ana please look\nat this", "nothing here", "@ana and @cy again")
	f := &fake{}
	done := MapDone{}
	people := []Person{{"ana", "ana@example.org"}, {"cy", "cy@example.org"}, {"dee", ""}}
	sent, err := Notify(context.Background(), f, s, "Plan\r\nBcc: x", "https://example.org/d/1", people, done)
	if err != nil || len(sent) != 2 {
		t.Fatalf("sent %v, err %v", sent, err)
	}
	if len(f.sent) != 2 || f.sent[0].To[0] != "ana@example.org" || !strings.Contains(f.sent[0].Subject, "2 times") || strings.ContainsAny(f.sent[0].Subject, "\r\n") {
		t.Errorf("mails: %+v", f.sent)
	}
	if b := f.sent[0].Text; !strings.Contains(b, "bo wrote: @ana please look at this") || !strings.Contains(b, "https://example.org/d/1") {
		t.Errorf("body:\n%s", b)
	}
	sent, _ = Notify(context.Background(), f, s, "Plan", "", people, done)
	if len(sent) != 0 || len(f.sent) != 2 {
		t.Error("a mention is not sent twice")
	}
	s.Add(canvas.Comment{ID: "z", Author: "bo", Text: "@ana one more"})
	sent, _ = Notify(context.Background(), f, s, "Plan", "", people, done)
	if len(sent) != 1 || !strings.Contains(f.sent[2].Subject, "mentioned in Plan") {
		t.Errorf("new one: %v %+v", sent, f.sent)
	}
}

func TestNotifyKeepsAFailureForNextTime(t *testing.T) {
	s := commented(t, "@ana hi", "@cy hi")
	f := &fake{fail: map[string]bool{"ana@example.org": true}}
	done := MapDone{}
	people := []Person{{"ana", "ana@example.org"}, {"cy", "cy@example.org"}}
	sent, err := Notify(context.Background(), f, s, "T", "", people, done)
	if err == nil || len(sent) != 1 || sent[0] != "cy" {
		t.Fatalf("sent %v err %v", sent, err)
	}
	f.fail = nil
	sent, err = Notify(context.Background(), f, s, "T", "", people, done)
	if err != nil || len(sent) != 1 || sent[0] != "ana" {
		t.Errorf("retry: %v %v", sent, err)
	}
	if _, err := Notify(context.Background(), nil, s, "T", "", people, done); !errors.Is(err, mail.ErrNotConfigured) {
		t.Errorf("nil sender: %v", err)
	}
}

func TestNotifyCapsAMailAndSendsTheRestNext(t *testing.T) {
	var texts []string
	for i := 0; i < MaxPerMail+3; i++ {
		texts = append(texts, "@ana "+strings.Repeat("x", 600))
	}
	s := commented(t, texts...)
	f := &fake{}
	done := MapDone{}
	people := []Person{{"ana", "ana@example.org"}}
	_, _ = Notify(context.Background(), f, s, "T", "", people, done)
	if !strings.Contains(f.sent[0].Text, "and 3 more") || strings.Contains(f.sent[0].Text, strings.Repeat("x", 501)) {
		t.Errorf("cap:\n%s", f.sent[0].Text)
	}
	_, _ = Notify(context.Background(), f, s, "T", "", people, done)
	if len(f.sent) != 2 || !strings.Contains(f.sent[1].Subject, "3 times") {
		t.Errorf("rest: %+v", f.sent)
	}
}
