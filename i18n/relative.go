package i18n

import "time"

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
