package components

// boolAttr renders b as the literal string ARIA state attributes
// expect ("aria-selected", "aria-expanded", ...), which -- unlike a
// native boolean attribute such as disabled -- must always be present
// with an explicit "true"/"false" value.
func boolAttr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
