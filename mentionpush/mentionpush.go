package mentionpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/mentionmail"
	"github.com/elaous/gojiberry/webpush"
)

// Sender sends one message to one subscription (webpush.Sender is one).
type Sender interface {
	Send(ctx context.Context, sub webpush.Subscription, payload []byte) error
}

type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag"`
	URL   string `json:"url,omitempty"`
}

func Notify(ctx context.Context, sender Sender, store webpush.Store, set *canvas.CommentSet, title, link string, handles []string, done mentionmail.Done) ([]string, error) {
	if sender == nil || store == nil {
		return nil, errors.New("mentionpush: no sender or store")
	}
	var reached []string
	var all error
	for _, h := range handles {
		entries, err := store.For(ctx, h)
		if err != nil {
			all = errors.Join(all, fmt.Errorf("mentionpush: %s: %w", h, err))
			continue
		}
		if len(entries) == 0 {
			continue
		}
		var fresh []canvas.MentionNotice
		for _, n := range set.MentionsOf(h) {
			if !done.Has(mentionmail.Key(mentionmail.Person{Handle: h}, n)) {
				fresh = append(fresh, n)
			}
		}
		if len(fresh) == 0 {
			continue
		}
		ok := false
		for _, e := range entries {
			mine := fresh[:0:0]
			for _, n := range fresh {
				if n.Created.IsZero() || !n.Created.Before(e.Added) {
					mine = append(mine, n)
				}
			}
			if len(mine) == 0 {
				continue
			}
			payload, _ := json.Marshal(build(h, title, link, mine))
			switch err := sender.Send(ctx, e.Subscription, payload); {
			case err == nil:
				ok = true
			case errors.Is(err, webpush.ErrGone):
				_ = store.Remove(ctx, e.Subscription.Endpoint)
			default:
				all = errors.Join(all, fmt.Errorf("mentionpush: %s: %w", h, err))
			}
		}
		if ok {
			for _, n := range fresh {
				done.Add(mentionmail.Key(mentionmail.Person{Handle: h}, n))
			}
			reached = append(reached, h)
		}
	}
	return reached, all
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func build(handle, title, link string, ns []canvas.MentionNotice) Message {
	t := clip(oneLine(title), 80)
	if t == "" {
		t = "a document"
	}
	m := Message{Tag: "mention-" + strings.ToLower(handle), URL: link}
	if len(ns) == 1 {
		who := clip(oneLine(ns[0].Author), 40)
		if who == "" {
			who = "Someone"
		}
		m.Title, m.Body = who+" mentioned you", "in "+t
	} else {
		m.Title, m.Body = fmt.Sprintf("%d mentions of you", len(ns)), "in "+t
	}
	return m
}
