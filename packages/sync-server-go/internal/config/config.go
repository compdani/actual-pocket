// Package config loads the server configuration from config.json and the
// ACTUAL_* environment variables.
//
// It is a port of packages/sync-server/src/load-config.js, minus every setting
// that belongs to the server's own auth system: PocketBase owns identity now.
package config

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Upload holds the per-route request body limits, in megabytes.
type Upload struct {
	FileSizeSyncLimitMB          int
	SyncEncryptedFileSizeLimitMB int
	FileSizeLimitMB              int
}

// Config is the fully resolved server configuration.
type Config struct {
	// Env is NODE_ENV: "production", "development" or "test".
	Env string
	// Mode is the value served by GET /mode: "test" or "development".
	Mode string
	// ProjectRoot is the absolute working directory of the process.
	ProjectRoot string
	// DataDir is the persistent data directory (default "./data").
	DataDir string
	// Port is the TCP port to listen on.
	Port int
	// Hostname is the address to listen on (default "::").
	Hostname string
	// ServerFiles locates the PocketBase data dir (default "<dataDir>/server-files").
	ServerFiles string
	// PBDataDir is "<serverFiles>/pb_data".
	PBDataDir string
	// WebRoot is the directory holding the built web client.
	WebRoot string
	// Upload holds the request body limits.
	Upload Upload
	// UserCreationMode is "manual" or "login"; it controls whether an OAuth2
	// login without a matching user may create one.
	UserCreationMode string
	// GithubToken is used by the plugin registry downloader.
	GithubToken string
	// CorsProxyEnabled toggles the /cors-proxy route.
	CorsProxyEnabled bool
	// TrustedProxies lists the CIDRs whose X-Forwarded-For hop is trusted when
	// resolving the client IP.
	TrustedProxies []netip.Prefix
	// SimpleFINAllowedNetworks lists private networks a SimpleFIN server may
	// live in.
	SimpleFINAllowedNetworks []netip.Prefix
	// MCPRedactSecrets hides sensitive fields in PocketBase REST/MCP
	// responses. Defaults to true.
	MCPRedactSecrets bool
	// ConfigPath is the config.json that was loaded, if any.
	ConfigPath string
	// Warnings lists settings that were found but are no longer supported.
	Warnings []string
}

// IsDev reports whether the server runs in development mode.
func (c *Config) IsDev() bool { return c.Env == "development" }

// IsTest reports whether the server runs in test mode.
func (c *Config) IsTest() bool { return c.Env == "test" }

// ListenAddr returns the host:port pair for the HTTP listener. Unlike the
// Node server's log line this is always a valid Go address, so an IPv6
// wildcard hostname becomes "[::]:5006".
func (c *Config) ListenAddr() string {
	return net.JoinHostPort(c.Hostname, strconv.Itoa(c.Port))
}

// Options overrides how the configuration is read. The zero value reads the
// real process environment and working directory.
type Options struct {
	// Env is the environment variable table to read. When nil, os.Environ is used.
	Env map[string]string
	// WorkingDir is the directory relative paths resolve against. When empty,
	// os.Getwd is used.
	WorkingDir string
	// Warn receives one message per setting that is set but no longer used.
	Warn func(string)
}

const (
	kindString = iota
	kindInt
	kindBool
	kindList
)

// fileSettings are the config.json keys this server still understands.
// Every other key is rejected, matching convict's `allowed: 'strict'`.
var fileSettings = []string{
	"env",
	"mode",
	"projectRoot",
	"dataDir",
	"port",
	"hostname",
	"serverFiles",
	"webRoot",
	"simplefinAllowedNetworks",
	"trustedProxies",
	"upload.fileSizeSyncLimitMB",
	"upload.syncEncryptedFileSizeLimitMB",
	"upload.fileSizeLimitMB",
	"userCreationMode",
	"mcpRedactSecrets",
	"github.token",
	"corsProxy.enabled",
	"https.key",
	"https.cert",
}

// droppedFileSettings are accepted (so an old config.json does not fail) but
// logged as unsupported.
var droppedFileSettings = []string{
	"userFiles",
	"loginMethod",
	"allowedLoginMethods",
	"trustedAuthProxies",
	"token_expiration",
	"enforceOpenId",
}

// droppedFilePrefixes covers the whole `openId` block.
var droppedFilePrefixes = []string{"openId."}

// droppedEnvVars are warned about when present.
var droppedEnvVars = []string{
	"ACTUAL_USER_FILES",
	"ACTUAL_LOGIN_METHOD",
	"ACTUAL_ALLOWED_LOGIN_METHODS",
	"ACTUAL_TRUSTED_AUTH_PROXIES",
	"ACTUAL_TOKEN_EXPIRATION",
	"ACTUAL_OPENID_DISCOVERY_URL",
	"ACTUAL_OPENID_PROVIDER_NAME",
	"ACTUAL_OPENID_AUTHORIZATION_ENDPOINT",
	"ACTUAL_OPENID_TOKEN_ENDPOINT",
	"ACTUAL_OPENID_USERINFO_ENDPOINT",
	"ACTUAL_OPENID_CLIENT_ID",
	"ACTUAL_OPENID_CLIENT_SECRET",
	"ACTUAL_OPENID_CLIENT_SECRET_FILE",
	"ACTUAL_OPENID_SERVER_HOSTNAME",
	"ACTUAL_OPENID_AUTH_METHOD",
	"ACTUAL_OPENID_ENFORCE",
}

// Load reads the configuration from the process environment and working directory.
func Load() (*Config, error) {
	return LoadWith(Options{})
}

// LoadWith reads the configuration using the given overrides.
func LoadWith(opts Options) (*Config, error) {
	env := opts.Env
	if env == nil {
		env = environ()
	}
	warn := opts.Warn
	if warn == nil {
		warn = func(string) {}
	}

	workingDir := opts.WorkingDir
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working directory: %w", err)
		}
	}
	projectRoot, err := filepath.Abs(workingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}

	// D12: PocketBase owns the listener, so custom TLS certificates are gone.
	for _, key := range []string{"ACTUAL_HTTPS_KEY", "ACTUAL_HTTPS_CERT"} {
		if v := env[key]; v != "" {
			return nil, fmt.Errorf(
				"%s is no longer supported: terminate TLS in a reverse proxy or run `serve --https=<domain>` to use PocketBase's built-in autocert",
				key,
			)
		}
	}

	cfg := &Config{
		Env:              "development",
		Mode:             "development",
		ProjectRoot:      projectRoot,
		Port:             5006,
		Hostname:         "::",
		Upload:           Upload{FileSizeSyncLimitMB: 20, SyncEncryptedFileSizeLimitMB: 50, FileSizeLimitMB: 20},
		UserCreationMode: "manual",
	}

	// NODE_ENV drives both `env` and mode.
	if v, ok := env["NODE_ENV"]; ok && v != "" {
		switch v {
		case "production", "development", "test":
			cfg.Env = v
		default:
			return nil, fmt.Errorf("env: invalid value %q (expected production, development or test)", v)
		}
	}
	if cfg.Env == "test" {
		cfg.Mode = "test"
	}

	defaultDataDir := filepath.Join(projectRoot, "data")
	if v, ok := env["ACTUAL_DATA_DIR"]; ok && v != "" {
		defaultDataDir = absPath(projectRoot, v)
	}

	// config.json lookup order: ACTUAL_CONFIG_PATH, then <projectRoot>/config.json,
	// then <dataDir>/config.json.
	configPath := ""
	if v, ok := env["ACTUAL_CONFIG_PATH"]; ok && v != "" {
		configPath = absPath(projectRoot, v)
		if _, err := os.Stat(configPath); err != nil {
			return nil, fmt.Errorf("read ACTUAL_CONFIG_PATH %q: %w", configPath, err)
		}
	} else {
		for _, candidate := range []string{
			filepath.Join(projectRoot, "config.json"),
			filepath.Join(defaultDataDir, "config.json"),
		} {
			if _, err := os.Stat(candidate); err == nil {
				configPath = candidate
				break
			}
		}
	}

	fileValues := map[string]any{}
	if configPath != "" {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("read config file %q: %w", configPath, err)
		}
		nested := map[string]any{}
		if err := json.Unmarshal(raw, &nested); err != nil {
			return nil, fmt.Errorf("parse config file %q: %w", configPath, err)
		}
		if err := flatten("", nested, fileValues); err != nil {
			return nil, err
		}
	}
	cfg.ConfigPath = configPath

	l := &loader{file: fileValues, env: env}
	if err := l.load(cfg, projectRoot, defaultDataDir); err != nil {
		return nil, err
	}

	// <VAR>_FILE overrides land after everything else, as in the Node loader.
	for _, fileEnvVar := range []string{"ACTUAL_GITHUB_TOKEN_FILE"} {
		value, present, err := readFileEnv(env, fileEnvVar)
		if err != nil {
			return nil, err
		}
		if present {
			cfg.GithubToken = value
		}
	}

	cfg.Warnings = l.warnings
	for _, name := range droppedEnvVars {
		if env[name] != "" {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s is no longer supported and was ignored", name))
		}
	}
	for _, w := range cfg.Warnings {
		warn(w)
	}

	return cfg, nil
}

// loader applies config.json values first and environment variables second, so
// that the environment always wins.
type loader struct {
	file     map[string]any
	env      map[string]string
	used     map[string]bool
	warnings []string
}

func (l *loader) load(cfg *Config, projectRoot, defaultDataDir string) error {
	var err error

	// dataDir and everything derived from it come first.
	cfg.DataDir = defaultDataDir
	if err := l.stringSetting("dataDir", "ACTUAL_DATA_DIR", projectRoot, &cfg.DataDir); err != nil {
		return err
	}
	cfg.DataDir = absPath(projectRoot, cfg.DataDir)

	cfg.ServerFiles = filepath.Join(cfg.DataDir, "server-files")
	if err := l.stringSetting("serverFiles", "ACTUAL_SERVER_FILES", projectRoot, &cfg.ServerFiles); err != nil {
		return err
	}
	cfg.ServerFiles = absPath(projectRoot, cfg.ServerFiles)
	cfg.PBDataDir = filepath.Join(cfg.ServerFiles, "pb_data")

	// The Node server resolves the web build next to its own package. The Go
	// binary keeps the same repository layout, relative to the working
	// directory it is started from.
	cfg.WebRoot = filepath.Join(projectRoot, "..", "desktop-client", "build")
	if err := l.stringSetting("webRoot", "ACTUAL_WEB_ROOT", projectRoot, &cfg.WebRoot); err != nil {
		return err
	}
	cfg.WebRoot = absPath(projectRoot, cfg.WebRoot)

	if raw, ok, err := l.get("env", "", kindString); err != nil {
		return err
	} else if ok {
		switch raw {
		case "production", "development", "test":
			// NODE_ENV, when set, already won in LoadWith; only fill in the gap.
			if _, envSet := l.env["NODE_ENV"]; !envSet {
				cfg.Env = raw
				if raw == "test" {
					cfg.Mode = "test"
				}
			}
		default:
			return fmt.Errorf("env: invalid value %q (expected production, development or test)", raw)
		}
	}

	// mode: default already set from NODE_ENV; config.json may override it.
	if raw, ok, err := l.get("mode", "", kindString); err != nil {
		return err
	} else if ok {
		switch raw {
		case "test", "development":
			cfg.Mode = raw
		default:
			return fmt.Errorf("mode: invalid value %q (expected test or development)", raw)
		}
	}

	// projectRoot is a read-only value in the Node schema; accept it so an old
	// config.json keeps loading.
	l.ignore("projectRoot")

	// PORT seeds the default, then ACTUAL_PORT and config.json override it.
	if v, ok := l.env["PORT"]; ok && v != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("PORT: expected a number, got %q", v)
		}
		cfg.Port = n
	}
	if err := l.intSetting("port", "ACTUAL_PORT", 0, 65535, &cfg.Port); err != nil {
		return err
	}
	if raw, ok, err := l.get("hostname", "ACTUAL_HOSTNAME", kindString); err != nil {
		return err
	} else if ok {
		cfg.Hostname = raw
	}

	if err := l.intSetting("upload.fileSizeSyncLimitMB", "ACTUAL_UPLOAD_FILE_SYNC_SIZE_LIMIT_MB", 0, math.MaxInt32, &cfg.Upload.FileSizeSyncLimitMB); err != nil {
		return err
	}
	if err := l.intSetting("upload.syncEncryptedFileSizeLimitMB", "ACTUAL_UPLOAD_SYNC_ENCRYPTED_FILE_SYNC_SIZE_LIMIT_MB", 0, math.MaxInt32, &cfg.Upload.SyncEncryptedFileSizeLimitMB); err != nil {
		return err
	}
	if err := l.intSetting("upload.fileSizeLimitMB", "ACTUAL_UPLOAD_FILE_SIZE_LIMIT_MB", 0, math.MaxInt32, &cfg.Upload.FileSizeLimitMB); err != nil {
		return err
	}

	if raw, ok, err := l.get("userCreationMode", "ACTUAL_USER_CREATION_MODE", kindString); err != nil {
		return err
	} else if ok {
		switch raw {
		case "manual", "login":
			cfg.UserCreationMode = raw
		default:
			return fmt.Errorf("userCreationMode: invalid value %q (expected manual or login)", raw)
		}
	}

	if cfg.GithubToken, err = l.stringValue("github.token", "ACTUAL_GITHUB_TOKEN"); err != nil {
		return err
	}
	if cfg.CorsProxyEnabled, err = l.boolSetting("corsProxy.enabled", "ACTUAL_CORS_PROXY_ENABLED"); err != nil {
		return err
	}
	if cfg.TrustedProxies, err = l.networks("trustedProxies", "ACTUAL_TRUSTED_PROXIES", []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"fc00::/7",
		"::1/128",
	}); err != nil {
		return err
	}
	if cfg.SimpleFINAllowedNetworks, err = l.networks("simplefinAllowedNetworks", "ACTUAL_SIMPLEFIN_ALLOWED_NETWORKS", nil); err != nil {
		return err
	}
	cfg.MCPRedactSecrets = true
	if value, ok, err := l.get("mcpRedactSecrets", "ACTUAL_MCP_REDACT_SECRETS", kindBool); err != nil {
		return err
	} else if ok {
		cfg.MCPRedactSecrets = value == "true"
	}

	// HTTPS is dropped (D12), even when configured through config.json.
	for _, key := range []string{"https.key", "https.cert"} {
		if v, ok := l.file[key]; ok {
			l.markUsed(key)
			if s, _ := v.(string); s != "" {
				return fmt.Errorf("%s is no longer supported: terminate TLS in a reverse proxy or run `serve --https=<domain>` to use PocketBase's built-in autocert", key)
			}
		}
	}

	for _, key := range droppedFileSettings {
		v, ok := l.file[key]
		if !ok {
			continue
		}
		l.markUsed(key)
		if !isEmptyValue(v) {
			l.warnings = append(l.warnings, fmt.Sprintf("config.json setting %q is no longer supported and was ignored", key))
		}
	}

	return l.checkUnknown()
}

// get returns the value of a setting, preferring the environment over config.json.
func (l *loader) get(key, envKey string, k int) (string, bool, error) {
	if envKey != "" {
		if v, ok := l.env[envKey]; ok {
			return v, true, nil
		}
	}
	v, ok := l.file[key]
	if !ok {
		return "", false, nil
	}
	l.markUsed(key)

	switch k {
	case kindString:
		s, ok := v.(string)
		if !ok {
			return "", false, fmt.Errorf("%s: expected a string", key)
		}
		return s, true, nil
	case kindInt:
		n, ok := v.(float64)
		if !ok || n != math.Trunc(n) {
			return "", false, fmt.Errorf("%s: expected an integer", key)
		}
		return strconv.FormatInt(int64(n), 10), true, nil
	case kindBool:
		b, ok := v.(bool)
		if !ok {
			return "", false, fmt.Errorf("%s: expected a boolean", key)
		}
		return strconv.FormatBool(b), true, nil
	case kindList:
		items, ok := v.([]any)
		if !ok {
			return "", false, fmt.Errorf("%s: expected an array of strings", key)
		}
		parts := make([]string, 0, len(items))
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				return "", false, fmt.Errorf("%s: expected an array of strings", key)
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ","), true, nil
	}

	return "", false, nil
}

func (l *loader) stringValue(key, envKey string) (string, error) {
	v, _, err := l.get(key, envKey, kindString)
	return v, err
}

func (l *loader) stringSetting(key, envKey, base string, dst *string) error {
	v, ok, err := l.get(key, envKey, kindString)
	if err != nil || !ok {
		return err
	}
	*dst = absPath(base, v)
	return nil
}

// intSetting reads an integer into dst, leaving dst untouched when neither the
// environment variable nor config.json sets it.
func (l *loader) intSetting(key, envKey string, min, max int, dst *int) error {
	v, ok, err := l.get(key, envKey, kindInt)
	if err != nil || !ok {
		return err
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("%s: expected a number, got %q", settingName(key, envKey), v)
	}
	if n < min || n > max {
		return fmt.Errorf("%s: value %d is out of range [%d, %d]", settingName(key, envKey), n, min, max)
	}
	*dst = n
	return nil
}

func (l *loader) boolSetting(key, envKey string) (bool, error) {
	v, ok, err := l.get(key, envKey, kindBool)
	if err != nil || !ok {
		return false, err
	}
	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%s: expected true or false, got %q", settingName(key, envKey), v)
}

func (l *loader) networks(key, envKey string, defaults []string) ([]netip.Prefix, error) {
	v, ok, err := l.get(key, envKey, kindList)
	if err != nil {
		return nil, err
	}
	if !ok {
		v = strings.Join(defaults, ",")
	}
	prefixes := make([]netip.Prefix, 0, 4)
	for _, entry := range strings.Split(v, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		prefix, err := parseNetwork(entry)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", settingName(key, envKey), err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func (l *loader) markUsed(key string) {
	if l.used == nil {
		l.used = map[string]bool{}
	}
	l.used["file:"+key] = true
}

func (l *loader) ignore(key string) {
	if _, ok := l.file[key]; ok {
		l.markUsed(key)
	}
}

func (l *loader) checkUnknown() error {
	known := map[string]bool{}
	for _, key := range fileSettings {
		known[key] = true
	}
	for _, key := range droppedFileSettings {
		known[key] = true
	}
	for key, value := range l.file {
		if known[key] || l.used["file:"+key] {
			continue
		}
		if hasPrefix(key, droppedFilePrefixes) {
			if !isEmptyValue(value) {
				l.warnings = append(l.warnings, fmt.Sprintf("config.json setting %q is no longer supported and was ignored", key))
			}
			continue
		}
		return fmt.Errorf("config.json: unknown setting %q", key)
	}
	return nil
}

func settingName(key, envKey string) string {
	if envKey != "" {
		return envKey
	}
	return key
}

func parseNetwork(entry string) (netip.Prefix, error) {
	if strings.Contains(entry, "/") {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("invalid network %q: %w", entry, err)
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid IP address %q: %w", entry, err)
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func hasPrefix(key string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func isEmptyValue(v any) bool {
	switch value := v.(type) {
	case nil:
		return true
	case string:
		return value == ""
	case bool:
		return !value
	case float64:
		return value == 0
	case []any:
		return len(value) == 0
	case map[string]any:
		return len(value) == 0
	}
	return false
}

func absPath(base, path string) string {
	if path == "" {
		return base
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// flatten turns nested config.json objects into the dotted keys convict uses.
func flatten(prefix string, values map[string]any, out map[string]any) error {
	for key, value := range values {
		full := key
		if prefix != "" {
			full = prefix + "." + key
		}
		if nested, ok := value.(map[string]any); ok {
			if err := flatten(full, nested, out); err != nil {
				return err
			}
			continue
		}
		out[full] = value
	}
	return nil
}

func environ() map[string]string {
	out := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}
