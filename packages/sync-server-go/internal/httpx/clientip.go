package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP mirrors Express's `req.ip` when `trust proxy` is set to a list of
// trusted CIDRs: walk the forwarding chain from the socket peer towards the
// client and return the first address that is not trusted by the operator.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	peer := remoteIP(r)
	if peer == "" {
		return ""
	}

	peerAddr, err := netip.ParseAddr(peer)
	if err != nil || !isTrusted(peerAddr, trusted) {
		return peer
	}

	// alladdrs in proxy-addr is [socket, xff last, xff second to last, ...].
	addrs := []string{peer}
	forwarded := forwardedFor(r)
	for i := len(forwarded) - 1; i >= 0; i-- {
		addrs = append(addrs, forwarded[i])
	}

	// Return the first untrusted hop, or the left-most one when every hop is
	// trusted.
	result := addrs[len(addrs)-1]
	for _, addr := range addrs[:len(addrs)-1] {
		parsed, err := netip.ParseAddr(addr)
		if err == nil && isTrusted(parsed, trusted) {
			continue
		}
		result = addr
		break
	}

	return result
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	addr = addr.Unmap()
	for _, prefix := range trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

func forwardedFor(r *http.Request) []string {
	var values []string
	for _, header := range r.Header.Values("X-Forwarded-For") {
		values = append(values, strings.Split(header, ",")...)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
