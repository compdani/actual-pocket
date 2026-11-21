package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func mustPrefixes(t *testing.T, values ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			t.Fatalf("parse %q: %v", value, err)
		}
		out = append(out, prefix)
	}
	return out
}

func TestClientIPWithoutProxy(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.113.5:1234"
	request.Header.Set("X-Forwarded-For", "1.2.3.4")

	// 203.0.113.5 is not in the trusted list, so the forwarded header is ignored.
	got := ClientIP(request, mustPrefixes(t, "10.0.0.0/8"))
	if got != "203.0.113.5" {
		t.Errorf("ClientIP = %q, want 203.0.113.5", got)
	}
}

func TestClientIPWalksForwardedChain(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.2")

	// The socket peer and 10.0.0.2 are trusted; 203.0.113.9 is the client.
	got := ClientIP(request, mustPrefixes(t, "127.0.0.0/8", "10.0.0.0/8"))
	if got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want 203.0.113.9", got)
	}
}

func TestClientIPStopsAtFirstUntrustedHop(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.9, 10.0.0.2")

	// 10.0.0.2 is trusted, 203.0.113.9 is not: a client spoofing the left-most
	// entry cannot move the reported address past the last real proxy.
	got := ClientIP(request, mustPrefixes(t, "127.0.0.0/8", "10.0.0.0/8"))
	if got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want 203.0.113.9", got)
	}
}

func TestClientIPAllHopsTrusted(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-For", "10.0.0.2, 10.0.0.3")

	got := ClientIP(request, mustPrefixes(t, "127.0.0.0/8", "10.0.0.0/8"))
	if got != "10.0.0.2" {
		t.Errorf("ClientIP = %q, want the left-most hop 10.0.0.2", got)
	}
}

func TestClientIPMultipleHeaderLines(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Add("X-Forwarded-For", "203.0.113.9")
	request.Header.Add("X-Forwarded-For", "10.0.0.2")

	got := ClientIP(request, mustPrefixes(t, "127.0.0.0/8", "10.0.0.0/8"))
	if got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want 203.0.113.9", got)
	}
}

func TestClientIPUnixSocket(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.1"

	got := ClientIP(request, nil)
	if got != "192.0.2.1" {
		t.Errorf("ClientIP = %q, want 192.0.2.1", got)
	}
}
