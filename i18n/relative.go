package i18n

import "time"

// Ago says how long ago something was, in lang: "just now", "5 minutes ago", "2 hours ago", "3 days
// ago". The texts are plural messages (see Plural) in the packs ("time.ago.minutes#one"); a language
// without them gets English. d is the time since the event; zero or negative counts as just now.
func (c *Catalog) Ago(lang string, d time.Duration) string {
	switch {
	case d < 45*time.Second:
		return c.Text(lang, "time.ago.now", "just now")
	case d < 60*time.Minute:
		n := int((d + 30*time.Second) / time.Minute)
		return c.Plural(lang, "time.ago.minutes", "%d minute ago", "%d minutes ago", max(n, 1))
	case d < 24*time.Hour:
		n := int((d + 30*time.Minute) / time.Hour)
		return c.Plural(lang, "time.ago.hours", "%d hour ago", "%d hours ago", max(n, 1))
	}
	n := int((d + 12*time.Hour) / (24 * time.Hour))
	return c.Plural(lang, "time.ago.days", "%d day ago", "%d days ago", max(n, 1))
}
