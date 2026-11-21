// Package ssrf blocks server-side requests to private and otherwise
// non-public addresses.
//
// The check runs in net.Dialer.Control, after DNS resolution and immediately
// before connect, so a hostname that later resolves to a private address
// (DNS rebinding) is rejected too. The classifications mirror
// packages/sync-server/src/util/ssrf.ts.
package ssrf

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// Options controls which private ranges may be reached.
type Options struct {
	// AllowPrivateNetwork permits every private class (private, loopback,
	// unique-local, CGNAT). Always-blocked ranges stay blocked.
	AllowPrivateNetwork bool
	// AllowedNetworks exempts only the listed private ranges. Link-local,
	// multicast, transition and metadata ranges stay blocked.
	AllowedNetworks []netip.Prefix
}

// ErrBlocked is returned when a connection targets a blocked address.
var ErrBlocked = errors.New("blocked request to private/local IP")

var (
	ipv4Private = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	ipv4Loopback    = netip.MustParsePrefix("127.0.0.0/8")
	ipv4LinkLocal   = netip.MustParsePrefix("169.254.0.0/16")
	ipv4CGNAT       = netip.MustParsePrefix("100.64.0.0/10")
	ipv6Loopback    = netip.MustParsePrefix("::1/128")
	ipv6LinkLocal   = netip.MustParsePrefix("fe80::/10")
	ipv6UniqueLocal = netip.MustParsePrefix("fc00::/7")
	nat64WellKnown  = netip.MustParsePrefix("64:ff9b::/96")
	nat64LocalUse   = netip.MustParsePrefix("64:ff9b:1::/48")
	ipv4Metadata    = []netip.Prefix{
		netip.MustParsePrefix("100.100.100.200/32"), // Alibaba
		netip.MustParsePrefix("168.63.129.16/32"),   // Azure wire server
		netip.MustParsePrefix("169.254.169.254/32"), // AWS/GCP/Azure metadata
		netip.MustParsePrefix("169.254.170.2/32"),   // ECS task metadata
	}
	alwaysBlocked = []netip.Prefix{
		// IPv4 special-purpose ranges.
		netip.MustParsePrefix("0.0.0.0/8"),       // unspecified and "this network"
		netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
		netip.MustParsePrefix("192.0.2.0/24"),    // documentation
		netip.MustParsePrefix("192.88.99.0/24"),  // 6to4 relay anycast
		netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
		netip.MustParsePrefix("198.51.100.0/24"), // documentation
		netip.MustParsePrefix("203.0.113.0/24"),  // documentation
		netip.MustParsePrefix("224.0.0.0/4"),     // multicast
		netip.MustParsePrefix("240.0.0.0/4"),     // reserved, includes broadcast
		// IPv6 special-purpose ranges.
		netip.MustParsePrefix("::/128"),         // unspecified
		netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
		netip.MustParsePrefix("100::/64"),       // discard-only
		netip.MustParsePrefix("2001::/32"),      // Teredo
		netip.MustParsePrefix("2001:2::/48"),    // benchmarking
		netip.MustParsePrefix("2001:10::/28"),   // ORCHID
		netip.MustParsePrefix("2001:20::/28"),   // ORCHIDv2
		netip.MustParsePrefix("2001:db8::/32"),  // documentation
		netip.MustParsePrefix("2002::/16"),      // 6to4 transition
		netip.MustParsePrefix("ff00::/8"),       // multicast
	}
)

// IsBlockedIP reports whether addr may not be contacted. Only literal IP
// addresses are classified; callers that may have a hostname should resolve it
// (or rely on the dialer) first.
func IsBlockedIP(addr netip.Addr, opts Options) bool {
	if !addr.IsValid() {
		return false
	}

	addr = addr.Unmap().WithZone("")

	if nat64WellKnown.Contains(addr) {
		// The well-known NAT64 prefix may only carry globally routable IPv4.
		bytes := addr.As16()
		embedded := netip.AddrFrom4([4]byte{bytes[12], bytes[13], bytes[14], bytes[15]})
		return classOf(embedded) != classPublic
	}

	class := classOf(addr)
	if class == classBlocked {
		return true
	}
	if class == classPublic {
		return false
	}

	// Link-local and metadata addresses are never exempt.
	if class == classLinkLocal || class == classMetadata {
		return true
	}

	if opts.AllowPrivateNetwork {
		return false
	}
	for _, network := range opts.AllowedNetworks {
		if network.Contains(addr) {
			return false
		}
	}

	return true
}

// IsBlockedHost reports whether a URL hostname is a blocked literal IP. Pure
// hostnames return false; the dialer check covers what they resolve to.
func IsBlockedHost(host string, opts Options) bool {
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return IsBlockedIP(addr, opts)
}

type ipClass int

const (
	classPublic ipClass = iota
	classPrivate
	classLoopback
	classLinkLocal
	classCGNAT
	classUniqueLocal
	classMetadata
	classBlocked
)

func classOf(addr netip.Addr) ipClass {
	if addr.Is4() {
		if contains(ipv4Metadata, addr) {
			return classMetadata
		}
		if ipv4Loopback.Contains(addr) {
			return classLoopback
		}
		if ipv4LinkLocal.Contains(addr) {
			return classLinkLocal
		}
		if ipv4CGNAT.Contains(addr) {
			return classCGNAT
		}
		if contains(ipv4Private, addr) {
			return classPrivate
		}
		if contains(alwaysBlocked, addr) {
			return classBlocked
		}
		return classPublic
	}

	if contains(ipv6Metadata(addr), addr) {
		return classMetadata
	}
	if ipv6Loopback.Contains(addr) {
		return classLoopback
	}
	if ipv6LinkLocal.Contains(addr) {
		return classLinkLocal
	}
	if ipv6UniqueLocal.Contains(addr) {
		return classUniqueLocal
	}
	if contains(alwaysBlocked, addr) {
		return classBlocked
	}

	return classPublic
}

func ipv6Metadata(addr netip.Addr) []netip.Prefix {
	// fd00:ec2::254 is the AWS IPv6 metadata endpoint; it falls inside the
	// unique-local range but must stay blocked as metadata.
	return []netip.Prefix{netip.MustParsePrefix("fd00:ec2::254/128")}
}

func contains(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// DialControl returns a net.Dialer.Control function that rejects blocked
// addresses at connect time.
func DialControl(opts Options) func(network, address string, conn syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("ssrf: split %q: %w", address, err)
		}
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("ssrf: parse %q: %w", host, err)
		}
		if IsBlockedIP(addr, opts) {
			return fmt.Errorf("%w: %s", ErrBlocked, addr)
		}
		return nil
	}
}

// NewTransport builds an HTTP transport whose dials are checked by the guard.
func NewTransport(opts Options) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   DialControl(opts),
	}

	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}
