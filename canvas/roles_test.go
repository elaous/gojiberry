package canvas

import "testing"

func TestRoles(t *testing.T) {
	for _, tc := range []struct {
		r                     Role
		edit, comment, manage bool
	}{
		{"", true, true, true}, {RoleOwner, true, true, true},
		{RoleEditor, true, true, false}, {RoleViewer, false, true, false},
	} {
		if tc.r.CanEdit() != tc.edit || tc.r.CanComment() != tc.comment || tc.r.CanManage() != tc.manage {
			t.Errorf("%q: %v %v %v", tc.r, tc.r.CanEdit(), tc.r.CanComment(), tc.r.CanManage())
		}
	}
	if ParseRole("") != RoleOwner || ParseRole("editor") != RoleEditor || ParseRole("admin") != RoleViewer {
		t.Fatal("ParseRole")
	}
	if Role("").String() != "owner" || RoleViewer.String() != "viewer" {
		t.Fatal("String")
	}
	if RoleViewer.Mode(ModeEdit) != ModeView || RoleEditor.Mode(ModeEdit) != ModeEdit || RoleViewer.Mode(ModeView) != ModeView {
		t.Fatal("Mode")
	}
}

func TestViewerCannotEdit(t *testing.T) {
	f, st := editFlow(t, 2, Chars{Text: "hello"})
	st.InsertText("x")
	if flowText(f) != "hexllo" {
		t.Fatalf("an owner edits: %q", flowText(f))
	}
	st.SetRole(RoleViewer)
	if f.Editing() || st.CanEdit() {
		t.Fatal("a viewer's components revert to view mode")
	}
	st.SetMode(ModeEdit) // asking again changes nothing
	f.MouseDown(10+8*2, 40)
	f.MouseUp(0, 0)
	st.InsertText("y")
	if flowText(f) != "hexllo" || f.Editing() {
		t.Fatalf("a viewer cannot edit: %q", flowText(f))
	}
	st.SetRole(RoleEditor)
	st.SetMode(ModeEdit)
	f.MouseDown(10+8*2, 40)
	f.MouseUp(0, 0)
	st.InsertText("z")
	if flowText(f) != "hezxllo" {
		t.Fatalf("an editor edits: %q", flowText(f))
	}
}

func TestViewerCanCommentWithoutEditing(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "hello brave world"})
	f.Author = "vee"
	st.SetRole(RoleViewer)
	if f.Editing() {
		t.Fatal("a viewer is not editing")
	}
	st.Prompt = func(string, string) (string, bool) { return "a viewer's note", true }
	if st.RunCommand(CmdCommentAdd) {
		t.Fatal("nothing selected: nothing to comment on")
	}
	f.SetCursors([]Cursor{{Anchor: 6, Caret: 11}})
	if !st.RunCommand(CmdCommentAdd) || f.Comments.Len() != 1 {
		t.Fatal("a viewer comments on selected text")
	}
	if got := f.CommentExcerpts()["c1"]; got != "brave" {
		t.Fatalf("on the selection: %q", got)
	}
	if flowText(f) != "hello brave world" {
		t.Fatalf("the text is untouched: %q", flowText(f))
	}
	if st.RunCommand(CmdBold) || st.RunCommand(CmdCommentDelete) || st.RunCommand(CmdBookmarkAdd) {
		t.Fatal("a viewer can only add comments")
	}
	tgt := f.CommentTarget()
	tgt.Replace([]Edit{{From: 0, To: 5, Items: []Item{Chars{Text: "HELLO"}}}})
	tgt.Replace([]Edit{{From: 0, To: 5, Items: []Item{Chars{Text: "hello", Format: Format{FontWeight: 700}}}}})
	tgt.Replace([]Edit{{From: 0, To: 0, Items: []Item{Chars{Text: "x"}}}})
	if flowText(f) != "hello brave world" {
		t.Fatalf("only the comment mark may change: %q", flowText(f))
	}
	for _, it := range f.ContentItems() {
		if ch, ok := it.(Chars); ok && ch.Format.FontWeight != 0 {
			t.Fatal("formatting was changed")
		}
	}
	if tgt.(CommentHost).CommentAuthor() != "vee" {
		t.Fatal("author")
	}
	g, st2 := editFlow(t, 0, Chars{Text: "abc"})
	st2.SetMode(ModeView)
	g.SetCursors([]Cursor{{Anchor: 0, Caret: 2}})
	st2.Prompt = func(string, string) (string, bool) { return "n", true }
	if !st2.RunCommand(CmdCommentAdd) {
		t.Fatal("view mode")
	}
	st2.SetRole(Role("nobody"))
	g.SetCursors([]Cursor{{Anchor: 0, Caret: 1}})
	if st2.RunCommand(CmdCommentAdd) {
		t.Fatal("an unknown role has no access")
	}
}

func TestScrollViewForwardsCommentTarget(t *testing.T) {
	f, p := selFlow(Chars{Text: "text"})
	sv := NewScrollView(0, 0, 100, 100)
	sv.Add(f, 100, 100)
	st := NewStage(p, 100, 100, sv)
	st.SetRole(RoleViewer)
	if sv.CommentTarget() != nil {
		t.Fatal("no selection")
	}
	f.SetCursors([]Cursor{{Anchor: 0, Caret: 2}})
	st.Prompt = func(string, string) (string, bool) { return "n", true }
	if !st.RunCommand(CmdCommentAdd) {
		t.Fatal("through a scroll view")
	}
}

func TestRemoteCursorsAreDrawn(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "hello world"})
	p := st.Painter.(*fakePainter)
	f.RemoteCursors = []RemoteCursor{{Name: "Ana", Color: "#f00", Anchor: 0, Caret: 5}, {Caret: 8}, {Name: "Bo", Anchor: 99, Caret: 99}}
	p.calls = nil
	f.Draw()
	for _, want := range []string{"fillStyle #f00", `fillText "Ana"`, "alpha 0.25", "alpha 1", "fillStyle " + DefaultPeerColor} {
		if !p.has(want) {
			t.Errorf("remote cursors lack %q", want)
		}
	}
	if p.has(`fillText "Bo"`) && !p.has(`fillText "Ana"`) {
		t.Fatal("flags")
	}
}
