package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func load(t *testing.T, dir string, env map[string]string) (*Config, error) {
	t.Helper()

	full := map[string]string{}
	for key, value := range env {
		full[key] = value
	}

	return LoadWith(Options{Env: full, WorkingDir: dir})
}

func TestDefaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := load(t, dir, nil)
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}

	if cfg.Env != "development" || cfg.Mode != "development" {
		t.Errorf("env/mode = %q/%q, want development/development", cfg.Env, cfg.Mode)
	}
	if cfg.ProjectRoot != dir {
		t.Errorf("ProjectRoot = %q, want %q", cfg.ProjectRoot, dir)
	}
	if want := filepath.Join(dir, "data"); cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
	if want := filepath.Join(dir, "data", "server-files"); cfg.ServerFiles != want {
		t.Errorf("ServerFiles = %q, want %q", cfg.ServerFiles, want)
	}
	if want := filepath.Join(dir, "data", "server-files", "pb_data"); cfg.PBDataDir != want {
		t.Errorf("PBDataDir = %q, want %q", cfg.PBDataDir, want)
	}
	if want := filepath.Clean(filepath.Join(dir, "..", "desktop-client", "build")); cfg.WebRoot != want {
		t.Errorf("WebRoot = %q, want %q", cfg.WebRoot, want)
	}
	if cfg.Port != 5006 {
		t.Errorf("Port = %d, want 5006", cfg.Port)
	}
	if cfg.Hostname != "::" {
		t.Errorf("Hostname = %q, want ::", cfg.Hostname)
	}
	if got := cfg.ListenAddr(); got != "[::]:5006" {
		t.Errorf("ListenAddr = %q, want [::]:5006", got)
	}
	if cfg.Upload.FileSizeSyncLimitMB != 20 ||
		cfg.Upload.SyncEncryptedFileSizeLimitMB != 50 ||
		cfg.Upload.FileSizeLimitMB != 20 {
		t.Errorf("Upload = %+v, want 20/50/20", cfg.Upload)
	}
	if cfg.UserCreationMode != "manual" {
		t.Errorf("UserCreationMode = %q, want manual", cfg.UserCreationMode)
	}
	if cfg.CorsProxyEnabled {
		t.Error("CorsProxyEnabled = true, want false")
	}
	if len(cfg.TrustedProxies) != 5 {
		t.Errorf("TrustedProxies = %v, want 5 entries", cfg.TrustedProxies)
	}
	if len(cfg.SimpleFINAllowedNetworks) != 0 {
		t.Errorf("SimpleFINAllowedNetworks = %v, want empty", cfg.SimpleFINAllowedNetworks)
	}
	if cfg.ConfigPath != "" {
		t.Errorf("ConfigPath = %q, want empty", cfg.ConfigPath)
	}
}

func TestEnvOverrides(t *testing.T) {
	dir := t.TempDir()

	cfg, err := load(t, dir, map[string]string{
		"NODE_ENV":                              "production",
		"ACTUAL_DATA_DIR":                       "state",
		"ACTUAL_PORT":                           "8080",
		"ACTUAL_HOSTNAME":                       "127.0.0.1",
		"ACTUAL_SERVER_FILES":                   "/srv/actual/server-files",
		"ACTUAL_WEB_ROOT":                       "/srv/actual/web",
		"ACTUAL_UPLOAD_FILE_SYNC_SIZE_LIMIT_MB": "5",
		"ACTUAL_UPLOAD_SYNC_ENCRYPTED_FILE_SYNC_SIZE_LIMIT_MB": "7",
		"ACTUAL_UPLOAD_FILE_SIZE_LIMIT_MB":                     "9",
		"ACTUAL_USER_CREATION_MODE":                            "login",
		"ACTUAL_CORS_PROXY_ENABLED":                            "true",
		"ACTUAL_TRUSTED_PROXIES":                               "127.0.0.1, 10.0.0.0/8",
		"ACTUAL_SIMPLEFIN_ALLOWED_NETWORKS":                    "192.168.1.0/24",
		"ACTUAL_GITHUB_TOKEN":                                  "from-env",
	})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}

	if cfg.Env != "production" || cfg.Mode != "development" {
		t.Errorf("env/mode = %q/%q, want production/development", cfg.Env, cfg.Mode)
	}
	if want := filepath.Join(dir, "state"); cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
	if cfg.ServerFiles != "/srv/actual/server-files" {
		t.Errorf("ServerFiles = %q", cfg.ServerFiles)
	}
	if cfg.PBDataDir != "/srv/actual/server-files/pb_data" {
		t.Errorf("PBDataDir = %q", cfg.PBDataDir)
	}
	if cfg.WebRoot != "/srv/actual/web" {
		t.Errorf("WebRoot = %q", cfg.WebRoot)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.ListenAddr() != "127.0.0.1:8080" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr())
	}
	if cfg.Upload != (Upload{FileSizeSyncLimitMB: 5, SyncEncryptedFileSizeLimitMB: 7, FileSizeLimitMB: 9}) {
		t.Errorf("Upload = %+v", cfg.Upload)
	}
	if cfg.UserCreationMode != "login" {
		t.Errorf("UserCreationMode = %q", cfg.UserCreationMode)
	}
	if !cfg.CorsProxyEnabled {
		t.Error("CorsProxyEnabled = false, want true")
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Errorf("TrustedProxies = %v, want 2 entries", cfg.TrustedProxies)
	}
	if len(cfg.SimpleFINAllowedNetworks) != 1 {
		t.Errorf("SimpleFINAllowedNetworks = %v, want 1 entry", cfg.SimpleFINAllowedNetworks)
	}
	if cfg.GithubToken != "from-env" {
		t.Errorf("GithubToken = %q", cfg.GithubToken)
	}
}

func TestPortFallbackToPORT(t *testing.T) {
	dir := t.TempDir()

	cfg, err := load(t, dir, map[string]string{"PORT": "7000"})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if cfg.Port != 7000 {
		t.Errorf("Port = %d, want 7000", cfg.Port)
	}

	cfg, err = load(t, dir, map[string]string{"PORT": "7000", "ACTUAL_PORT": "7001"})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if cfg.Port != 7001 {
		t.Errorf("Port = %d, want ACTUAL_PORT to win (7001)", cfg.Port)
	}
}

func TestConfigFileFromProjectRoot(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, filepath.Join(dir, "config.json"), `{
		"port": 6000,
		"hostname": "0.0.0.0",
		"mode": "test",
		"upload": {"fileSizeLimitMB": 33},
		"github": {"token": "from-file"}
	}`)

	cfg, err := load(t, dir, nil)
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}

	if cfg.ConfigPath != filepath.Join(dir, "config.json") {
		t.Errorf("ConfigPath = %q", cfg.ConfigPath)
	}
	if cfg.Port != 6000 {
		t.Errorf("Port = %d, want 6000", cfg.Port)
	}
	if cfg.Hostname != "0.0.0.0" {
		t.Errorf("Hostname = %q", cfg.Hostname)
	}
	if cfg.Mode != "test" {
		t.Errorf("Mode = %q, want test", cfg.Mode)
	}
	if cfg.Upload.FileSizeLimitMB != 33 {
		t.Errorf("FileSizeLimitMB = %d, want 33", cfg.Upload.FileSizeLimitMB)
	}
	if cfg.GithubToken != "from-file" {
		t.Errorf("GithubToken = %q", cfg.GithubToken)
	}
}

func TestConfigFileFromDataDir(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(dataDir, "config.json"), `{"port": 6001}`)

	cfg, err := load(t, dir, map[string]string{"ACTUAL_DATA_DIR": "state"})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}

	if cfg.ConfigPath != filepath.Join(dataDir, "config.json") {
		t.Errorf("ConfigPath = %q", cfg.ConfigPath)
	}
	if cfg.Port != 6001 {
		t.Errorf("Port = %d, want 6001", cfg.Port)
	}
}

func TestConfigPathEnvWins(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom.json")
	writeConfig(t, custom, `{"port": 6002}`)
	writeConfig(t, filepath.Join(dir, "config.json"), `{"port": 6003}`)

	cfg, err := load(t, dir, map[string]string{"ACTUAL_CONFIG_PATH": custom})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if cfg.Port != 6002 {
		t.Errorf("Port = %d, want 6002", cfg.Port)
	}
}

func TestMissingConfigPathIsAnError(t *testing.T) {
	dir := t.TempDir()

	_, err := load(t, dir, map[string]string{"ACTUAL_CONFIG_PATH": filepath.Join(dir, "nope.json")})
	if err == nil {
		t.Fatal("expected an error for a missing ACTUAL_CONFIG_PATH")
	}
}

func TestEnvBeatsConfigFile(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, filepath.Join(dir, "config.json"), `{"port": 6000}`)

	cfg, err := load(t, dir, map[string]string{"ACTUAL_PORT": "6001"})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if cfg.Port != 6001 {
		t.Errorf("Port = %d, want 6001", cfg.Port)
	}
}

func TestFileEnvBeatsPlainEnv(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("  from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := load(t, dir, map[string]string{
		"ACTUAL_GITHUB_TOKEN":      "from-env",
		"ACTUAL_GITHUB_TOKEN_FILE": tokenFile,
	})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if cfg.GithubToken != "from-file" {
		t.Errorf("GithubToken = %q, want the trimmed file contents", cfg.GithubToken)
	}
}

func TestUnreadableFileEnvIsAnError(t *testing.T) {
	dir := t.TempDir()

	_, err := load(t, dir, map[string]string{"ACTUAL_GITHUB_TOKEN_FILE": filepath.Join(dir, "nope")})
	if err == nil || !strings.Contains(err.Error(), "ACTUAL_GITHUB_TOKEN_FILE") {
		t.Fatalf("err = %v, want a readable ACTUAL_GITHUB_TOKEN_FILE error", err)
	}
}

func TestUnknownConfigKeyIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, filepath.Join(dir, "config.json"), `{"nope": true}`)

	_, err := load(t, dir, nil)
	if err == nil || !strings.Contains(err.Error(), `unknown setting "nope"`) {
		t.Fatalf("err = %v, want an unknown setting error", err)
	}
}

func TestDroppedSettingsWarn(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, filepath.Join(dir, "config.json"), `{
		"userFiles": "/tmp/user-files",
		"loginMethod": "header",
		"allowedLoginMethods": ["password", "header"],
		"trustedAuthProxies": ["10.0.0.0/8"],
		"token_expiration": "never",
		"enforceOpenId": true,
		"openId": {"client_id": "abc", "issuer": {"name": "test"}}
	}`)

	var warnings []string
	cfg, err := LoadWith(Options{
		Env:        map[string]string{"ACTUAL_OPENID_CLIENT_SECRET": "secret", "ACTUAL_USER_FILES": "/tmp/x"},
		WorkingDir: dir,
		Warn:       func(msg string) { warnings = append(warnings, msg) },
	})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}

	for _, key := range []string{"userFiles", "loginMethod", "allowedLoginMethods", "trustedAuthProxies", "token_expiration", "enforceOpenId", "openId.client_id", "openId.issuer.name"} {
		if !slices.ContainsFunc(cfg.Warnings, func(w string) bool {
			return strings.Contains(w, key)
		}) {
			t.Errorf("no warning for dropped config.json key %q (warnings: %v)", key, cfg.Warnings)
		}
	}
	for _, name := range []string{"ACTUAL_OPENID_CLIENT_SECRET", "ACTUAL_USER_FILES"} {
		if !slices.ContainsFunc(cfg.Warnings, func(w string) bool {
			return strings.Contains(w, name)
		}) {
			t.Errorf("no warning for dropped env var %q (warnings: %v)", name, cfg.Warnings)
		}
	}
	if len(warnings) != len(cfg.Warnings) {
		t.Errorf("Warn called %d times for %d warnings", len(warnings), len(cfg.Warnings))
	}
}

func TestHTTPSConfigIsAnError(t *testing.T) {
	dir := t.TempDir()

	for _, key := range []string{"ACTUAL_HTTPS_KEY", "ACTUAL_HTTPS_CERT"} {
		_, err := load(t, dir, map[string]string{key: "pem"})
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: err = %v, want a clear unsupported error", key, err)
		}
	}

	writeConfig(t, filepath.Join(dir, "config.json"), `{"https": {"key": "pem"}}`)
	if _, err := load(t, dir, nil); err == nil {
		t.Error("config.json https.key: expected an error")
	}
}

func TestInvalidValues(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"NODE_ENV", map[string]string{"NODE_ENV": "staging"}, "env"},
		{"port", map[string]string{"ACTUAL_PORT": "nope"}, "ACTUAL_PORT"},
		{"port range", map[string]string{"ACTUAL_PORT": "70000"}, "out of range"},
		{"upload", map[string]string{"ACTUAL_UPLOAD_FILE_SIZE_LIMIT_MB": "-1"}, "out of range"},
		{"user creation", map[string]string{"ACTUAL_USER_CREATION_MODE": "auto"}, "userCreationMode"},
		{"bool", map[string]string{"ACTUAL_CORS_PROXY_ENABLED": "yes"}, "ACTUAL_CORS_PROXY_ENABLED"},
		{"network", map[string]string{"ACTUAL_TRUSTED_PROXIES": "not-an-ip"}, "ACTUAL_TRUSTED_PROXIES"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(t, dir, tc.env)
			if err == nil {
				t.Fatalf("expected an error for %v", tc.env)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestConfigFileTypeErrors(t *testing.T) {
	dir := t.TempDir()

	for name, body := range map[string]string{
		"string for int":  `{"port": "6000"}`,
		"string for bool": `{"corsProxy": {"enabled": "true"}}`,
		"scalar for list": `{"trustedProxies": "10.0.0.0/8"}`,
	} {
		t.Run(name, func(t *testing.T) {
			writeConfig(t, filepath.Join(dir, "config.json"), body)
			if _, err := load(t, dir, nil); err == nil {
				t.Fatalf("expected an error for %s", body)
			}
		})
	}
}

func TestSingleIPBecomesFullPrefix(t *testing.T) {
	dir := t.TempDir()

	cfg, err := load(t, dir, map[string]string{"ACTUAL_TRUSTED_PROXIES": "127.0.0.1,192.168.0.0/16"})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if got := cfg.TrustedProxies[0].String(); got != "127.0.0.1/32" {
		t.Errorf("first prefix = %q, want 127.0.0.1/32", got)
	}
	if got := cfg.TrustedProxies[1].String(); got != "192.168.0.0/16" {
		t.Errorf("second prefix = %q, want 192.168.0.0/16", got)
	}
}

func TestTestEnvSetsTestMode(t *testing.T) {
	dir := t.TempDir()

	cfg, err := load(t, dir, map[string]string{"NODE_ENV": "test"})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if cfg.Mode != "test" {
		t.Errorf("Mode = %q, want test", cfg.Mode)
	}
}

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
