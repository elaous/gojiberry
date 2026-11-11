package clientkey

import (
	"net/netip"
	"strings"
)

func Of(addr string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil {
		s := strings.ToLower(strings.TrimSpace(addr))
		if len(s) > 64 {
			s = s[:64]
		}
		return s
	}
	a = a.WithZone("").Unmap()
	if a.Is4() {
		return a.String()
	}
	p, err := a.Prefix(64)
	if err != nil {
		return a.String()
	}
	return p.Masked().String()
}
