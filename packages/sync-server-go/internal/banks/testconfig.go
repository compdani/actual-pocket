package banks

import (
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
)

const testModeEnv = "ACTUAL_TEST_MODE"

// TestConfig holds the provider base-URL overrides that are only honoured when
// ACTUAL_TEST_MODE=1. They exist so the Playwright acceptance script can point
// the providers at local fakes.
type TestConfig struct {
	Enabled              bool
	SimpleFINBaseURL     *url.URL
	EnableBankingBaseURL string
	PluggyBaseURL        string
	AkahuBaseURL         string
	SimpleFINAllowedNets []netip.Prefix
}

func loadTestConfig(logger *slog.Logger) TestConfig {
	if logger == nil {
		logger = slog.Default()
	}

	names := []string{
		"ACTUAL_TEST_SIMPLEFIN_BASE_URL",
		"ACTUAL_TEST_ENABLEBANKING_BASE_URL",
		"ACTUAL_TEST_PLUGGY_BASE_URL",
		"ACTUAL_TEST_AKAHU_BASE_URL",
	}
	values := map[string]string{}
	anySet := false
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			values[name] = value
			anySet = true
		}
	}

	config := TestConfig{}
	if os.Getenv(testModeEnv) != "1" {
		if anySet {
			logger.Warn("ignoring provider base URL overrides: ACTUAL_TEST_MODE is not 1",
				"env", names)
		}
		return config
	}

	config.Enabled = true
	if raw := values["ACTUAL_TEST_ENABLEBANKING_BASE_URL"]; raw != "" {
		if parsed, err := url.Parse(raw); err == nil && parsed.IsAbs() {
			config.EnableBankingBaseURL = strings.TrimRight(raw, "/")
		} else {
			logger.Warn("ignoring invalid ACTUAL_TEST_ENABLEBANKING_BASE_URL", "value", raw)
		}
	}
	if raw := values["ACTUAL_TEST_PLUGGY_BASE_URL"]; raw != "" {
		if parsed, err := url.Parse(raw); err == nil && parsed.IsAbs() {
			config.PluggyBaseURL = strings.TrimRight(raw, "/")
		} else {
			logger.Warn("ignoring invalid ACTUAL_TEST_PLUGGY_BASE_URL", "value", raw)
		}
	}
	if raw := values["ACTUAL_TEST_AKAHU_BASE_URL"]; raw != "" {
		if parsed, err := url.Parse(raw); err == nil && parsed.IsAbs() {
			config.AkahuBaseURL = strings.TrimRight(raw, "/")
		} else {
			logger.Warn("ignoring invalid ACTUAL_TEST_AKAHU_BASE_URL", "value", raw)
		}
	}

	if raw := values["ACTUAL_TEST_SIMPLEFIN_BASE_URL"]; raw != "" {
		if parsed, err := url.Parse(raw); err == nil && parsed.IsAbs() {
			config.SimpleFINBaseURL = parsed
			config.SimpleFINAllowedNets = loopbackNetworks(parsed.Hostname())
		} else {
			logger.Warn("ignoring invalid ACTUAL_TEST_SIMPLEFIN_BASE_URL", "value", raw)
		}
	}

	return config
}

// loopbackNetworks returns only the prefixes that can reach the override host.
func loopbackNetworks(host string) []netip.Prefix {
	if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		addr = addr.Unmap()
		bits := 32
		if addr.Is6() {
			bits = 128
		}
		return []netip.Prefix{netip.PrefixFrom(addr, bits)}
	}

	if host == "localhost" {
		return []netip.Prefix{
			netip.MustParsePrefix("127.0.0.0/8"),
			netip.MustParsePrefix("::1/128"),
		}
	}

	addresses, err := net.LookupHost(host)
	if err != nil {
		return nil
	}
	prefixes := make([]netip.Prefix, 0, len(addresses))
	for _, address := range addresses {
		addr, err := netip.ParseAddr(address)
		if err != nil {
			continue
		}
		addr = addr.Unmap()
		bits := 32
		if addr.Is6() {
			bits = 128
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, bits))
	}
	return prefixes
}
