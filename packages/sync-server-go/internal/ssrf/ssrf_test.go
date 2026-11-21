package ssrf

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func prefix(t *testing.T, raw string) netip.Prefix {
	t.Helper()
	parsed, err := netip.ParsePrefix(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestIsBlockedIP(t *testing.T) {
	cases := []struct {
		address string
		opts    Options
		want    bool
	}{
		{"8.8.8.8", Options{}, false},
		{"1.1.1.1", Options{}, false},
		{"2606:4700:4700::1111", Options{}, false},

		{"10.0.0.1", Options{}, true},
		{"172.16.1.1", Options{}, true},
		{"192.168.1.1", Options{}, true},
		{"127.0.0.1", Options{}, true},
		{"169.254.1.1", Options{}, true},
		{"100.64.0.1", Options{}, true},
		{"fc00::1", Options{}, true},
		{"fd12::1", Options{}, true},
		{"::1", Options{}, true},
		{"fe80::1", Options{}, true},
		{"224.0.0.1", Options{}, true},
		{"255.255.255.255", Options{}, true},
		{"0.0.0.0", Options{}, true},
		{"::", Options{}, true},
		{"2002:a9fe:a9fe::", Options{}, true},
		{"64:ff9b:1::1", Options{}, true},

		// IPv4-mapped addresses are classified as their IPv4 form.
		{"::ffff:127.0.0.1", Options{}, true},
		{"::ffff:8.8.8.8", Options{}, false},

		// NAT64 may only carry public IPv4.
		{"64:ff9b::808:808", Options{}, false},
		{"64:ff9b::a9fe:a9fe", Options{}, true},
		{"64:ff9b::a00:1", Options{}, true},

		// Metadata addresses stay blocked even when their class is allowed.
		{"100.100.100.200", Options{AllowedNetworks: []netip.Prefix{prefix(t, "100.64.0.0/10")}}, true},
		{"fd00:ec2::254", Options{AllowPrivateNetwork: true}, true},

		// Link-local is never exempt.
		{"169.254.1.1", Options{AllowedNetworks: []netip.Prefix{prefix(t, "169.254.0.0/16")}}, true},
		{"fe80::1", Options{AllowedNetworks: []netip.Prefix{prefix(t, "fe80::/10")}}, true},

		// Allowed networks exempt only the matching private address.
		{"192.168.1.1", Options{AllowedNetworks: []netip.Prefix{prefix(t, "192.168.0.0/16")}}, false},
		{"192.168.2.1", Options{AllowedNetworks: []netip.Prefix{prefix(t, "192.168.0.0/16")}}, false},
		{"10.0.0.1", Options{AllowedNetworks: []netip.Prefix{prefix(t, "192.168.0.0/16")}}, true},
		{"127.0.0.1", Options{AllowedNetworks: []netip.Prefix{prefix(t, "127.0.0.0/8")}}, false},

		// AllowPrivateNetwork permits private classes, not blocked ranges.
		{"192.168.1.1", Options{AllowPrivateNetwork: true}, false},
		{"100.64.0.1", Options{AllowPrivateNetwork: true}, false},
		{"fc00::1", Options{AllowPrivateNetwork: true}, false},
	}

	for _, testCase := range cases {
		addr := netip.MustParseAddr(testCase.address)
		if got := IsBlockedIP(addr, testCase.opts); got != testCase.want {
			t.Errorf("IsBlockedIP(%s, %+v) = %v, want %v", testCase.address, testCase.opts, got, testCase.want)
		}
		if got := IsBlockedHost(testCase.address, testCase.opts); got != testCase.want {
			t.Errorf("IsBlockedHost(%s, %+v) = %v, want %v", testCase.address, testCase.opts, got, testCase.want)
		}
	}

	if IsBlockedHost("example.com", Options{}) {
		t.Error("IsBlockedHost(hostname) = true, want false")
	}
}

func TestTransportChecksAtConnectTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)

	client := &http.Client{Transport: NewTransport(Options{}), Timeout: 5 * time.Second}
	if _, err := client.Get(server.URL); err == nil {
		t.Fatal("strict transport reached loopback")
	}

	allowed := &http.Client{
		Transport: NewTransport(Options{AllowedNetworks: []netip.Prefix{prefix(t, "127.0.0.0/8")}}),
		Timeout:   5 * time.Second,
	}
	response, err := allowed.Get(server.URL)
	if err != nil {
		t.Fatalf("allowed transport failed: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d", response.StatusCode)
	}
}
