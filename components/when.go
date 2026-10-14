package components

import "time"

// FormatTime is how the components write the time something happened (a version, a comment, a
// suggestion, a document's last change). It gives "2006-01-02 15:04" (with ":05" when seconds is true)
// until an app sets it, which is where the viewer's language and time zone belong:
//
//	components.FormatTime = func(t time.Time, seconds bool) string { return i18n.DateTime(lang, t.Local()) }
var FormatTime = func(t time.Time, seconds bool) string {
	if seconds {
		return t.Format("2006-01-02 15:04:05")
	}
	return t.Format("2006-01-02 15:04")
}
