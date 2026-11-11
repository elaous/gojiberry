package canvas

import (
	"strings"
	"testing"
)

type fakeRoster map[string]string // address -> handle

func (r fakeRoster) HandleOf(a string) (string, bool) { h, ok := r[strings.ToLower(a)]; return h, ok }
func (r fakeRoster) Has(h string) bool {
	for _, v := range r {
		if v == h {
			return true
		}
	}
	return false
}

func TestCheckMentionsKeepsAddressesOutOfComments(t *testing.T) {
	s := NewCommentSet()
	s.Roster = fakeRoster{"ana@example.com": "ana", "bo@example.com": "bo"}
	clean, problems := s.CheckMentions("Ask ana@example.com and @bo, then @zed")
	if clean != "Ask @ana and @bo, then @zed" {
		t.Errorf("clean = %q", clean)
	}
	if len(problems) != 1 || problems[0].Kind != MentionUnknown || problems[0].Blocks() {
		t.Errorf("problems = %+v, want one hint about @zed", problems)
	}
	clean, problems = s.CheckMentions("cc eve@example.org")
	p, blocked := BlockingMention(problems)
	if !blocked || p.Mention.Name != "eve@example.org" || clean != "cc eve@example.org" {
		t.Errorf("an address with no access: clean %q problems %+v", clean, problems)
	}
	if m := p.Message(); !strings.Contains(m, "eve@example.org has no access") || !strings.Contains(m, "Share the document") {
		t.Errorf("message = %q", m)
	}
	plain := NewCommentSet()
	if c, pr := plain.CheckMentions("ask eve@example.org"); c != "ask eve@example.org" || pr != nil {
		t.Errorf("without a roster: %q %v", c, pr)
	}
}

func TestVisibleHidesAddressesFromReaders(t *testing.T) {
	s := NewCommentSet()
	s.Add(Comment{ID: "c1", Text: "see eve@example.org and ana@example.com", Replies: []Reply{{Text: "or bo@example.com"}}})
	if got := s.Visible()[0].Text; !strings.Contains(got, "eve@example.org") {
		t.Fatalf("without a roster the text is as written: %q", got)
	}
	s.Roster = fakeRoster{"ana@example.com": "ana", "bo@example.com": "bo"}
	v := s.Visible()[0]
	if v.Text != "see @someone and @ana" || v.Replies[0].Text != "or @bo" {
		t.Errorf("visible = %q / %q", v.Text, v.Replies[0].Text)
	}
	if got, _ := s.Get("c1"); !strings.Contains(got.Text, "eve@example.org") {
		t.Error("the stored comment was changed")
	}
}

func TestCommentCommandAsksAgainForAnAddressWithNoAccess(t *testing.T) {
	f, st := commentFlow(t)
	f.CommentSet().Roster = fakeRoster{"ana@example.com": "ana"}
	f.SetCursors([]Cursor{{Anchor: 6, Caret: 11}})
	var asked []string
	answers := []string{"look eve@example.org", "look ana@example.com"}
	st.Prompt = func(msg, initial string) (string, bool) {
		asked = append(asked, msg+"|"+initial)
		a := answers[0]
		answers = answers[1:]
		return a, true
	}
	if !st.RunCommand(CmdCommentAdd) {
		t.Fatal("the comment was not made")
	}
	if len(asked) != 2 || !strings.Contains(asked[1], "eve@example.org has no access") || !strings.HasSuffix(asked[1], "|look eve@example.org") {
		t.Errorf("asked = %q", asked)
	}
	if l := f.Comments.List(); len(l) != 1 || l[0].Text != "look @ana" {
		t.Errorf("comments = %+v", l)
	}
}
