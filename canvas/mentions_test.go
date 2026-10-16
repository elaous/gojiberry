package canvas

import "testing"

type testRoster map[string]string

func (r testRoster) HandleOf(a string) (string, bool) { h, ok := r[a]; return h, ok }
func (r testRoster) Has(h string) bool {
	for _, v := range r {
		if v == h {
			return true
		}
	}
	return false
}

func TestMentionsOfListsWhatOthersWroteAboutThePerson(t *testing.T) {
	s := NewCommentSet(
		Comment{ID: "c1", Author: "bo@example.com", Text: "@ana please check this"},
		Comment{ID: "c2", Author: "ana@example.com", Text: "@ana note to self"},
		Comment{ID: "c3", Author: "bo@example.com", Text: "nothing here", Replies: []Reply{
			{Author: "cy@example.com", Text: "agree with @Ana"},
			{Author: "ana@example.com", Text: "thanks @bo"},
			{Author: "bo@example.com", Text: "ping @anabel"}}},
	)
	s.Roster = testRoster{"ana@example.com": "ana", "bo@example.com": "bo", "cy@example.com": "cy"}
	got := s.MentionsOf("ana")
	if len(got) != 2 || got[0].Key() != "c1#-1" || got[1].Key() != "c3#0" || got[1].Author != "cy" {
		t.Fatalf("%+v", got)
	}
	if n := s.NewMentions("@ana", map[string]bool{"c1#-1": true}); len(n) != 1 || n[0].Key() != "c3#0" {
		t.Fatalf("%+v", n)
	}
	if len((*CommentSet)(nil).MentionsOf("ana")) != 0 || len(s.MentionsOf("")) != 0 {
		t.Fatal("nobody to tell")
	}
}
