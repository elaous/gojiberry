package canvas

import "testing"

func TestRoles(t *testing.T) {
	for _, tc := range []struct {
		r                     Role
		edit, comment, manage bool
	}{
		{"", true, true, true}, {RoleOwner, true, true, true},
		{RoleEditor, true, true, false}, {RoleViewer, false, false, false},
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
