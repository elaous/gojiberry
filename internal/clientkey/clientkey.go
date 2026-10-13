// Package clientkey names the client behind an address for rate limits: an IPv4 address as it is, an IPv6
// address by its /64.
package clientkey

import (
	"net/netip"
	"strings"
)

// Of names the client behind addr: an IPv4 address as it is, an IPv6 address by its /64 (one subscriber is
// usually given a whole /64, so rotating through its addresses buys nothing). Anything that is not an
// address is lowercased and cut to 64 bytes, so it cannot make a long key either.
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
