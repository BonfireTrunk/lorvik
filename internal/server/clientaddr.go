package server

import (
	"context"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

// caller is who a public sign-in request came from, for the limits and the
// security log.
type caller struct {
	addr      netip.Addr
	userAgent string
}

type callerKey struct{}

func withCaller(ctx context.Context, c caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// callerFrom returns the caller the sign-in guard stored; the zero caller
// when the request did not pass it.
func callerFrom(ctx context.Context) caller {
	c, _ := ctx.Value(callerKey{}).(caller)
	return c
}

// proxies are the reverse proxies whose X-Forwarded-For is believed.
type proxies []netip.Prefix

func (p proxies) trusted(a netip.Addr) bool {
	for _, pre := range p {
		if pre.Contains(a) {
			return true
		}
	}
	return false
}

// clientAddr is the address a request came from. That is the peer of the
// connection, unless the peer is a trusted proxy: then it is the nearest
// address in X-Forwarded-For that is not one. The header is read from the
// right because a proxy appends the address it saw, so everything to the
// left of the first untrusted hop is whatever the client chose to send.
func (p proxies) clientAddr(r *http.Request) netip.Addr {
	peer := parseAddr(r.RemoteAddr)
	if !peer.IsValid() || !p.trusted(peer) {
		return peer
	}
	client := peer
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for _, hop := range slices.Backward(hops) {
		a := parseAddr(strings.TrimSpace(hop))
		if !a.IsValid() {
			break
		}
		client = a
		if !p.trusted(a) {
			break
		}
	}
	return client
}

// parseAddr reads "host:port" or a bare address; the zero Addr when it is
// neither.
func parseAddr(s string) netip.Addr {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap().WithZone("")
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap().WithZone("")
}

// limitAddr is the address a limit counts under. An IPv6 host is usually
// handed a whole /64, so counting single addresses there would give it
// 2^64 separate allowances.
func limitAddr(a netip.Addr) string {
	if a.Is6() {
		if p, err := a.Prefix(64); err == nil {
			return p.String()
		}
	}
	return a.String()
}
