package banks

import (
	"bytes"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
)

func TestTestConfigRequiresFlag(t *testing.T) {
	t.Setenv(testModeEnv, "0")
	t.Setenv("ACTUAL_TEST_PLUGGY_BASE_URL", "http://127.0.0.1:9999")
	t.Setenv("ACTUAL_TEST_SIMPLEFIN_BASE_URL", "http://127.0.0.1:9998")

	buffer := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buffer, nil))
	config := loadTestConfig(logger)

	if config.Enabled || config.PluggyBaseURL != "" || config.SimpleFINBaseURL != nil {
		t.Errorf("overrides honoured without ACTUAL_TEST_MODE: %+v", config)
	}
	if !strings.Contains(buffer.String(), "ignoring provider base URL overrides") {
		t.Errorf("warning not logged: %s", buffer.String())
	}
}

func TestTestConfigParsesOverrides(t *testing.T) {
	t.Setenv(testModeEnv, "1")
	t.Setenv("ACTUAL_TEST_ENABLEBANKING_BASE_URL", "http://127.0.0.1:7001/")
	t.Setenv("ACTUAL_TEST_PLUGGY_BASE_URL", "http://127.0.0.1:7002")
	t.Setenv("ACTUAL_TEST_AKAHU_BASE_URL", "http://127.0.0.1:7003")
	t.Setenv("ACTUAL_TEST_SIMPLEFIN_BASE_URL", "http://127.0.0.1:7004")

	config := loadTestConfig(nil)
	if !config.Enabled {
		t.Fatal("test mode not enabled")
	}
	if config.EnableBankingBaseURL != "http://127.0.0.1:7001" ||
		config.PluggyBaseURL != "http://127.0.0.1:7002" ||
		config.AkahuBaseURL != "http://127.0.0.1:7003" {
		t.Errorf("base URLs = %+v", config)
	}
	if config.SimpleFINBaseURL == nil || config.SimpleFINBaseURL.Host != "127.0.0.1:7004" {
		t.Errorf("simplefin URL = %v", config.SimpleFINBaseURL)
	}
	if len(config.SimpleFINAllowedNets) != 1 || config.SimpleFINAllowedNets[0] != netip.MustParsePrefix("127.0.0.1/32") {
		t.Errorf("simplefin allowed networks = %v", config.SimpleFINAllowedNets)
	}

	if got := loopbackNetworks("localhost"); len(got) != 2 {
		t.Errorf("localhost networks = %v", got)
	}
	if got := loopbackNetworks("nonexistent.invalid"); len(got) != 0 {
		t.Errorf("non-local host networks = %v", got)
	}
}
