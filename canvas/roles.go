package canvas

// Role is what a user may do with a document; the zero value is owner.
type Role string

const (
	// RoleOwner may do everything: edit, comment and manage who has access.
	RoleOwner Role = "owner"
	// RoleEditor may edit and comment.
	RoleEditor Role = "editor"
	// RoleViewer may read and comment, but not edit.
	RoleViewer Role = "viewer"
)

// ParseRole turns a name into a Role; "" means owner.
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

// CanComment reports whether the role may comment; every role may.
func (r Role) CanComment() bool {
	return r == "" || r == RoleOwner || r == RoleEditor || r == RoleViewer
}

// CanManage reports whether the role may change who has access.
func (r Role) CanManage() bool { return r == "" || r == RoleOwner }

// Mode returns the mode a role allows for the wanted mode.
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

// SetRole sets the Stage's role; non-editing roles force view mode.
func (s *Stage) SetRole(r Role) {
	s.Role = r
	if !r.CanEdit() {
		s.SetMode(ModeView)
	}
}

// CanEdit reports whether the Stage's role allows editing.
func (s *Stage) CanEdit() bool { return s.Role.CanEdit() }
