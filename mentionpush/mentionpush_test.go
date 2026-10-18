package mentionpush

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/mentionmail"
	"github.com/elaous/gojiberry/webpush"
)

type fake struct {
	sent []struct {
		to  string
		msg Message
	}
	err map[string]error
}

func (f *fake) Send(_ context.Context, sub webpush.Subscription, payload []byte) error {
	if e := f.err[sub.Endpoint]; e != nil {
		return e
	}
	var m Message
	_ = json.Unmarshal(payload, &m)
	f.sent = append(f.sent, struct {
		to  string
		msg Message
	}{sub.Endpoint, m})
	return nil
}

func sub(endpoint string) webpush.Subscription { return webpush.Subscription{Endpoint: endpoint} }

func comments(t *testing.T, at time.Time, texts ...string) *canvas.CommentSet {
	t.Helper()
	s := canvas.NewCommentSet()
	for i, x := range texts {
		s.Add(canvas.Comment{ID: string(rune('a' + i)), Author: "bo", Text: x, Created: at})
	}
	return s
}

func TestNotifyPushesWhoAndWhereNeverTheText(t *testing.T) {
	now := time.Now()
	st := &webpush.MemStore{Now: func() time.Time { return now.Add(-time.Hour) }}
	_ = st.Add(context.Background(), "ana", sub("https://p.example/1"))
	_ = st.Add(context.Background(), "ana", sub("https://p.example/2"))
	s := comments(t, now, "@ana the secret figure is 42")
	f := &fake{}
	done := mentionmail.MapDone{}
	reached, err := Notify(context.Background(), f, st, s, "Plan\nBcc", "/d/plan", []string{"ana", "cy"}, done)
	if err != nil || len(reached) != 1 || reached[0] != "ana" || len(f.sent) != 2 {
		t.Fatalf("reached %v, %v, sent %d", reached, err, len(f.sent))
	}
	m := f.sent[0].msg
	if m.Title != "bo mentioned you" || m.Body != "in Plan Bcc" || m.URL != "/d/plan" || m.Tag != "mention-ana" {
		t.Errorf("message %+v", m)
	}
	if strings.Contains(m.Title+m.Body, "42") {
		t.Error("the text of the comment is in the notification")
	}
	reached, _ = Notify(context.Background(), f, st, s, "Plan", "/d/plan", []string{"ana"}, done)
	if len(reached) != 0 || len(f.sent) != 2 {
		t.Error("a mention is not pushed twice")
	}
	s.Add(canvas.Comment{ID: "x", Author: "bo", Text: "@ana again", Created: now})
	s.Add(canvas.Comment{ID: "y", Author: "cy", Text: "@ana and again", Created: now})
	_, _ = Notify(context.Background(), f, st, s, "Plan", "", []string{"ana"}, done)
	if got := f.sent[len(f.sent)-1].msg.Title; got != "2 mentions of you" {
		t.Errorf("several: %q", got)
	}
}

func TestOnlyMentionsAfterTheSubscriptionAreSent(t *testing.T) {
	now := time.Now()
	st := &webpush.MemStore{Now: func() time.Time { return now }}
	_ = st.Add(context.Background(), "ana", sub("https://p.example/1"))
	s := comments(t, now.Add(-time.Hour), "@ana long ago")
	f := &fake{}
	reached, _ := Notify(context.Background(), f, st, s, "Plan", "", []string{"ana"}, mentionmail.MapDone{})
	if len(reached) != 0 || len(f.sent) != 0 {
		t.Errorf("an old mention was pushed: %v %d", reached, len(f.sent))
	}
}

func TestAGoneSubscriptionIsDroppedAndAFailureIsRetried(t *testing.T) {
	now := time.Now()
	st := &webpush.MemStore{Now: func() time.Time { return now.Add(-time.Hour) }}
	_ = st.Add(context.Background(), "ana", sub("https://p.example/gone"))
	_ = st.Add(context.Background(), "ana", sub("https://p.example/ok"))
	_ = st.Add(context.Background(), "cy", sub("https://p.example/down"))
	s := comments(t, now, "@ana hi", "@cy hi")
	f := &fake{err: map[string]error{"https://p.example/gone": webpush.ErrGone, "https://p.example/down": errors.New("503")}}
	done := mentionmail.MapDone{}
	reached, err := Notify(context.Background(), f, st, s, "T", "", []string{"ana", "cy"}, done)
	if err == nil || len(reached) != 1 || reached[0] != "ana" {
		t.Fatalf("reached %v, %v", reached, err)
	}
	if l, _ := st.For(context.Background(), "ana"); len(l) != 1 || l[0].Subscription.Endpoint != "https://p.example/ok" {
		t.Errorf("the gone subscription stays: %+v", l)
	}
	f.err = nil
	reached, err = Notify(context.Background(), f, st, s, "T", "", []string{"ana", "cy"}, done)
	if err != nil || len(reached) != 1 || reached[0] != "cy" {
		t.Errorf("retry: %v %v", reached, err)
	}
	if _, err := Notify(context.Background(), nil, st, s, "T", "", nil, done); err == nil {
		t.Error("no sender")
	}
}
