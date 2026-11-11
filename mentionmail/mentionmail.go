package mentionmail

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/mail"
)

// Person is somebody who can be mentioned and reached.
type Person struct {
	Handle, Address string
}

// Done remembers which mentions were already sent.
type Done interface {
	Has(key string) bool
	Add(key string)
}

type MapDone map[string]bool

func (m MapDone) Has(k string) bool { return m[k] }
func (m MapDone) Add(k string)      { m[k] = true }

// Limits of one mail.
const (
	MaxPerMail   = 20
	MaxTextRunes = 500
)

func Key(p Person, n canvas.MentionNotice) string { return strings.ToLower(p.Handle) + "|" + n.Key() }

func Notify(ctx context.Context, sender mail.Sender, set *canvas.CommentSet, title, link string, people []Person, done Done) ([]string, error) {
	if sender == nil {
		return nil, mail.ErrNotConfigured
	}
	var sent []string
	var first error
	for _, p := range people {
		if strings.TrimSpace(p.Address) == "" {
			continue
		}
		var fresh []canvas.MentionNotice
		for _, n := range set.MentionsOf(p.Handle) {
			if !done.Has(Key(p, n)) {
				fresh = append(fresh, n)
			}
		}
		if len(fresh) == 0 {
			continue
		}
		batch := fresh
		if len(batch) > MaxPerMail {
			batch = batch[:MaxPerMail] // the rest go in the next mail
		}
		err := sender.Send(ctx, mail.Message{To: []string{p.Address}, Subject: subject(title, len(batch)), Text: body(title, link, p, batch, len(fresh)-len(batch))})
		if err != nil {
			first = errors.Join(first, fmt.Errorf("mentionmail: %s: %w", p.Handle, err))
			continue
		}
		for _, n := range batch {
			done.Add(Key(p, n))
		}
		sent = append(sent, p.Handle)
	}
	return sent, first
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func subject(title string, n int) string {
	t := oneLine(title)
	if t == "" {
		t = "a document"
	}
	if r := []rune(t); len(r) > 100 {
		t = string(r[:100]) + "…"
	}
	if n == 1 {
		return "You were mentioned in " + t
	}
	return fmt.Sprintf("You were mentioned %d times in %s", n, t)
}

func body(title, link string, p Person, ns []canvas.MentionNotice, more int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hello @%s,\n\n", strings.TrimPrefix(p.Handle, "@"))
	fmt.Fprintf(&b, "You were mentioned in %q:\n", oneLine(title))
	for _, n := range ns {
		t := oneLine(n.Text)
		if r := []rune(t); len(r) > MaxTextRunes {
			t = string(r[:MaxTextRunes]) + "…"
		}
		fmt.Fprintf(&b, "\n%s wrote: %s\n", oneLine(n.Author), t)
	}
	if more > 0 {
		fmt.Fprintf(&b, "\nand %d more, in the next mail.\n", more)
	}
	if l := strings.TrimSpace(link); l != "" {
		fmt.Fprintf(&b, "\nOpen the document: %s\n", oneLine(l))
	}
	return b.String()
}
