package canvas

// Role is what a user may do with a document. The zero value is
// RoleOwner, so a Stage with no role set behaves as it always did. The
// names match docstore's access levels ("editor", "viewer"; the owner is
// the ACL's Owner).
type Role string

const (
	// RoleOwner may do everything: edit, comment and manage who has access.
	RoleOwner Role = "owner"
	// RoleEditor may edit and comment.
	RoleEditor Role = "editor"
	// RoleViewer may only read: every component is in view mode, whatever
	// mode is asked for.
	RoleViewer Role = "viewer"
)

// ParseRole turns a name into a Role: "" is the zero value's RoleOwner,
// and a name that is not a role gets the least access, RoleViewer.
func ParseRole(s string) Role {
	switch Role(s) {
	case RoleOwner, RoleEditor, RoleViewer:
		return Role(s)
	case "":
		return RoleOwner
	}
	return RoleViewer
}

// CanEdit reports whether the role may edit the document.
func (r Role) CanEdit() bool { return r == "" || r == RoleOwner || r == RoleEditor }

// CanComment reports whether the role may add and change comments.
func (r Role) CanComment() bool { return r.CanEdit() }

// CanManage reports whether the role may change who has access.
func (r Role) CanManage() bool { return r == "" || r == RoleOwner }

// Mode returns the mode a component may be in when want is asked for: a
// role that cannot edit is held to ModeView.
func (r Role) Mode(want Mode) Mode {
	if want == ModeEdit && !r.CanEdit() {
		return ModeView
	}
	return want
}

// String returns the role's name ("owner" for the zero value).
func (r Role) String() string {
	if r == "" {
		return string(RoleOwner)
	}
	return string(r)
}

// SetRole sets the Stage's role. A role that cannot edit puts every
// component back in view mode now (ending any editing), and SetMode keeps
// them there.
func (s *Stage) SetRole(r Role) {
	s.Role = r
	if !r.CanEdit() {
		s.SetMode(ModeView)
	}
}

// CanEdit reports whether the Stage's role allows editing.
func (s *Stage) CanEdit() bool { return s.Role.CanEdit() }
