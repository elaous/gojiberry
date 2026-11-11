package export

import "strings"

const softBreakRune = ' '

func rawCodeText(segs []Segment) string {
	var sb strings.Builder
	for _, seg := range segs {
		if seg.SoftBreak {
			sb.WriteRune(softBreakRune)
			continue
		}
		sb.WriteString(seg.Text)
	}
	return sb.String()
}
