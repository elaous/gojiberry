package components

import "time"

var FormatTime = func(t time.Time, seconds bool) string {
	if seconds {
		return t.Format("2006-01-02 15:04:05")
	}
	return t.Format("2006-01-02 15:04")
}
