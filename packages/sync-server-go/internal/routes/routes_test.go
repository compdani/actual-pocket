package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
)

type testServer struct {
	handler http.Handler
	logs    *bytes.Buffer
}

func newTestServer(t *testing.T, cfg *config.Config, version string) *testServer {
	t.Helper()

	logs := &bytes.Buffer{}
	r := router.NewRouter(func(w http.ResponseWriter, req *http.Request) (*core.RequestEvent, router.EventCleanupFunc) {
		return &core.RequestEvent{Event: router.Event{Response: w, Request: req}}, nil
	})

	Register(&core.ServeEvent{Router: r}, Options{
		Config:  cfg,
		Version: version,
		Stdout:  logs,
	})

	mux, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}

	return &testServer{handler: mux, logs: logs}
}

func (s *testServer) get(path string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	return recorder
}

func productionConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.LoadWith(config.Options{
		Env:        map[string]string{"NODE_ENV": "production"},
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestMode(t *testing.T) {
	cfg := productionConfig(t)
	cfg.Mode = "development"
	server := newTestServer(t, cfg, "1.2.3")

	response := server.get("/mode", nil)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}
	if got, want := response.Header().Get("Content-Type"), "text/html; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := response.Body.String(), "development"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestHealth(t *testing.T) {
	server := newTestServer(t, productionConfig(t), "1.2.3")

	response := server.get("/health", nil)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}
	if got, want := response.Body.String(), `{"status":"UP"}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got, want := response.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}

	// Same bytes and ETag as the Node server.
	etag := response.Header().Get("ETag")
	notModified := server.get("/health", map[string]string{"If-None-Match": etag})
	if notModified.Code != http.StatusNotModified {
		t.Errorf("conditional status = %d, want 304", notModified.Code)
	}
}

func TestInfo(t *testing.T) {
	server := newTestServer(t, productionConfig(t), "26.11.0")

	response := server.get("/info", nil)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}
	want := `{"build":{"name":"@actual-app/sync-server","description":"actual syncing server","version":"26.11.0"}}`
	if got := response.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	// Verified against the Node server with `curl -i`.
	if got, wantTag := response.Header().Get("ETag"), `W/"66-B9e4EuuD2z6Lf3ONckIPXfQsRBQ"`; got != wantTag {
		t.Errorf("ETag = %q, want %q", got, wantTag)
	}
}

func TestMetrics(t *testing.T) {
	server := newTestServer(t, productionConfig(t), "1.2.3")

	response := server.get("/metrics", nil)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}

	pattern := regexp.MustCompile(`^\{"mem":\{"rss":\d+,"heapTotal":\d+,"heapUsed":\d+,"external":0,"arrayBuffers":0\},"uptime":\d+(\.\d+)?\}$`)
	if got := response.Body.String(); !pattern.MatchString(got) {
		t.Errorf("body = %q, want the Node memoryUsage shape", got)
	}

	var decoded struct {
		Mem    map[string]uint64
		Uptime float64
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"rss", "heapTotal", "heapUsed", "external", "arrayBuffers"} {
		if _, ok := decoded.Mem[key]; !ok {
			t.Errorf("missing mem.%s", key)
		}
	}
	if decoded.Uptime <= 0 {
		t.Errorf("uptime = %v, want > 0", decoded.Uptime)
	}
}

func TestRequestLog(t *testing.T) {
	server := newTestServer(t, productionConfig(t), "1.2.3")

	server.get("/health", nil)
	server.get("/mode", nil)

	lines := strings.Split(strings.TrimSpace(server.logs.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %v, want 2", lines)
	}
	if lines[0] != "GET 200 /health" {
		t.Errorf("first line = %q", lines[0])
	}
	if lines[1] != "GET 200 /mode" {
		t.Errorf("second line = %q", lines[1])
	}
}

func TestGlobalLimiterHeaders(t *testing.T) {
	server := newTestServer(t, productionConfig(t), "1.2.3")

	response := server.get("/health", nil)
	if got := response.Header().Get("RateLimit-Limit"); got != "500" {
		t.Errorf("RateLimit-Limit = %q, want 500", got)
	}
	if got := response.Header().Get("RateLimit-Policy"); got != "500;w=60" {
		t.Errorf("RateLimit-Policy = %q, want 500;w=60", got)
	}
}

func TestGlobalLimiterDisabledInDevelopment(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.LoadWith(config.Options{
		Env:        map[string]string{"NODE_ENV": "development"},
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, cfg, "1.2.3")

	response := server.get("/health", nil)
	if got := response.Header().Get("RateLimit-Limit"); got != "" {
		t.Errorf("RateLimit-Limit = %q, want no limiter headers in development", got)
	}
}

func TestSecurityHeadersOnlyOnTheFrontend(t *testing.T) {
	cfg := productionConfig(t)
	cfg.WebRoot = writeWebRoot(t)
	server := newTestServer(t, cfg, "1.2.3")

	frontend := server.get("/", nil)
	if got := frontend.Header().Get("Cross-Origin-Opener-Policy"); got != "same-origin" {
		t.Errorf("COOP = %q, want same-origin", got)
	}
	if got := frontend.Header().Get("Cross-Origin-Embedder-Policy"); got != "require-corp" {
		t.Errorf("COEP = %q, want require-corp", got)
	}
	wantCSP := "default-src 'self' blob:; img-src 'self' blob: data:; script-src 'self' 'unsafe-eval' blob:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; connect-src http: https:"
	if got := frontend.Header().Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("CSP = %q, want %q", got, wantCSP)
	}

	health := server.get("/health", nil)
	for _, header := range []string{"Cross-Origin-Opener-Policy", "Cross-Origin-Embedder-Policy", "Content-Security-Policy"} {
		if got := health.Header().Get(header); got != "" {
			t.Errorf("%s on /health = %q, want it only on frontend responses", header, got)
		}
	}
}

func TestStaticServesTheSPA(t *testing.T) {
	cfg := productionConfig(t)
	cfg.WebRoot = writeWebRoot(t)
	server := newTestServer(t, cfg, "1.2.3")

	index := server.get("/", nil)
	if index.Code != 200 {
		t.Fatalf("/ status = %d", index.Code)
	}
	if !strings.Contains(index.Body.String(), "<title>Actual</title>") {
		t.Errorf("/ body = %q, want index.html", index.Body.String())
	}

	asset := server.get("/assets/app.js", nil)
	if asset.Code != 200 {
		t.Fatalf("/assets/app.js status = %d", asset.Code)
	}
	if got := asset.Body.String(); got != "console.log('actual');\n" {
		t.Errorf("asset body = %q", got)
	}

	// Unknown paths fall back to index.html so client-side routing works.
	fallback := server.get("/accounts/123/budget", nil)
	if fallback.Code != 200 || !strings.Contains(fallback.Body.String(), "<title>Actual</title>") {
		t.Errorf("SPA fallback = %d %q", fallback.Code, fallback.Body.String())
	}
}

func TestStaticDoesNotShadowPocketBaseRoutes(t *testing.T) {
	cfg := productionConfig(t)
	cfg.WebRoot = writeWebRoot(t)

	logs := &bytes.Buffer{}
	r := router.NewRouter(func(w http.ResponseWriter, req *http.Request) (*core.RequestEvent, router.EventCleanupFunc) {
		return &core.RequestEvent{Event: router.Event{Response: w, Request: req}}, nil
	})
	// The PocketBase API group is registered by apis.NewRouter before our
	// group; a stand-in is enough to prove the static catch-all loses.
	r.GET("/api/health", func(e *core.RequestEvent) error {
		return e.String(200, "pocketbase")
	})
	Register(&core.ServeEvent{Router: r}, Options{Config: cfg, Version: "1.2.3", Stdout: logs})

	mux, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if got := recorder.Body.String(); got != "pocketbase" {
		t.Errorf("/api/health = %q, want the PocketBase route to win", got)
	}
}

func TestDevModeProxiesToVite(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/some/page" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>vite</html>"))
	}))
	defer upstream.Close()

	// Register captures the proxy target, so redirect before registering.
	redirectDevServer(t, upstream.URL)

	dir := t.TempDir()
	cfg, err := config.LoadWith(config.Options{
		Env:        map[string]string{"NODE_ENV": "development"},
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	logs := &bytes.Buffer{}
	r := router.NewRouter(func(w http.ResponseWriter, req *http.Request) (*core.RequestEvent, router.EventCleanupFunc) {
		return &core.RequestEvent{Event: router.Event{Response: w, Request: req}}, nil
	})
	Register(&core.ServeEvent{Router: r}, Options{Config: cfg, Version: "1.2.3", Stdout: logs})

	mux, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(mux)
	defer proxy.Close()

	response, err := http.Get(proxy.URL + "/some/page")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(response.Body)
	if !strings.Contains(body.String(), "vite") {
		t.Errorf("body = %q, want the proxied response", body.String())
	}
	if got := response.Header.Get("Content-Security-Policy"); !strings.Contains(got, "ws:") {
		t.Errorf("CSP = %q, want the development variant", got)
	}
}

// redirectDevServer points the development proxy at a test upstream.
func redirectDevServer(t *testing.T, url string) {
	t.Helper()
	previous := devServerURL
	devServerURL = url
	t.Cleanup(func() { devServerURL = previous })
}

func writeWebRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "index.html"), "<!doctype html><html><head><title>Actual</title></head><body></body></html>")
	writeFile(t, filepath.Join(root, "assets", "app.js"), "console.log('actual');\n")

	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
