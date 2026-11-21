package routes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/ssrf"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func proxyResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type corsFake struct {
	mu        sync.Mutex
	allowlist string
	allowErr  error
	responses map[string]*http.Response
	errors    map[string]error
	seen      []*http.Request
	allowHits int
}

func (f *corsFake) roundTrip(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.seen = append(f.seen, request)
	if request.URL.String() == corsAllowlistURL {
		f.allowHits++
		allowErr := f.allowErr
		allowlist := f.allowlist
		f.mu.Unlock()
		if allowErr != nil {
			return nil, allowErr
		}
		return proxyResponse(200, "application/json", allowlist), nil
	}
	target := request.URL.String()
	response := f.responses[target]
	err := f.errors[target]
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if response != nil {
		return response, nil
	}
	return proxyResponse(200, "text/plain", "default response"), nil
}

func (f *corsFake) lastRequest() *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) == 0 {
		return nil
	}
	return f.seen[len(f.seen)-1]
}

func (f *corsFake) resetSeen() {
	f.mu.Lock()
	f.seen = nil
	f.allowHits = 0
	f.mu.Unlock()
}

func newCORSHarness(t *testing.T) (*harness, *corsFake) {
	t.Helper()

	h := newHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.CorsProxyEnabled = true
	}, true)

	fake := &corsFake{
		allowlist: `[{"url":"https://github.com/user/repo1"},{"url":"https://github.com/user/repo2"}]`,
		responses: map[string]*http.Response{},
		errors:    map[string]error{},
	}

	corsProxyRateLimit = 1000
	corsAllowlistURL = "https://allowlist.test/plugins.json"
	corsProxyClient = &http.Client{Transport: roundTripFunc(fake.roundTrip)}
	resetCORSCache()

	t.Cleanup(func() {
		resetCORSCache()
		corsProxyRateLimit = 25
		corsAllowlistURL = pluginStoreURL
		corsProxyClient = &http.Client{
			Transport: ssrf.NewTransport(ssrf.Options{}),
			Timeout:   60 * time.Second,
		}
	})

	return h, fake
}

func (h *harness) corsGet(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/cors-proxy/?url="+url.QueryEscape(target), nil)
	request.Header.Set("X-Actual-Token", h.userToken)
	return h.do(request)
}

func (h *harness) corsGetBody(t *testing.T, target string, body any) *httptest.ResponseRecorder {
	t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/cors-proxy/?url="+url.QueryEscape(target), bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Actual-Token", h.userToken)
	return h.do(request)
}

func TestCORSProxyNotRegisteredWhenDisabled(t *testing.T) {
	h := newHarness(t)

	request := httptest.NewRequest(http.MethodOptions, "/cors-proxy/?url=https://example.com", nil)
	response := h.do(request)
	if response.Code == http.StatusNoContent {
		t.Fatal("OPTIONS /cors-proxy was handled while corsProxy.enabled is false")
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Fatal("CORS headers were set while corsProxy.enabled is false")
	}
}

func TestCORSProxyPreflight(t *testing.T) {
	h, _ := newCORSHarness(t)

	request := httptest.NewRequest(http.MethodOptions, "/cors-proxy/?url=https://example.com", nil)
	response := h.do(request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("allow origin = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Methods"); got != "GET,HEAD,OPTIONS" {
		t.Errorf("allow methods = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, X-Actual-Token" {
		t.Errorf("allow headers = %q", got)
	}
	if got := response.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("max age = %q", got)
	}
}

func TestCORSProxyValidation(t *testing.T) {
	h, _ := newCORSHarness(t)

	// Missing url is checked before the session.
	response := h.getJSON("/cors-proxy/", "")
	if response.Code != http.StatusBadRequest || jsonError(t, response)["error"] != "Missing url parameter" {
		t.Errorf("missing url: %d %q", response.Code, response.Body.String())
	}

	response = h.corsGet(t, "invalid-url")
	if response.Code != http.StatusBadRequest || jsonError(t, response)["error"] != "Invalid url parameter" {
		t.Errorf("invalid url: %d %q", response.Code, response.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/cors-proxy/?url=https://github.com/user/repo1", nil)
	response = h.do(request)
	if response.Code != http.StatusUnauthorized {
		t.Errorf("no session: status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestCORSProxyAllowlist(t *testing.T) {
	h, fake := newCORSHarness(t)

	for _, target := range []string{
		"https://github.com/user/repo1",
		"https://github.com/user/repo1/releases/download/v1/file.zip",
		"https://api.github.com/repos/user/repo1",
		"https://api.github.com/repos/user/repo1/releases",
		"https://raw.githubusercontent.com/user/repo1/main/file.txt",
	} {
		response := h.corsGet(t, target)
		if response.Code != http.StatusOK {
			t.Errorf("%s: status = %d, body = %q", target, response.Code, response.Body.String())
		}
	}

	for _, target := range []string{
		"http://192.168.1.1/test",
		"http://127.0.0.1/test",
		"https://api.github.com/repos/user/repo1-private/contents/.env",
		"https://github.com/user/repo2-private",
		"https://example.com",
	} {
		response := h.corsGet(t, target)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, body = %q", target, response.Code, response.Body.String())
		}
	}

	// The allowlist is cached between requests.
	resetCORSCache()
	fake.resetSeen()
	for range 3 {
		if response := h.corsGet(t, "https://github.com/user/repo1"); response.Code != http.StatusOK {
			t.Fatalf("status = %d", response.Code)
		}
	}
	fake.mu.Lock()
	hits := fake.allowHits
	fake.mu.Unlock()
	if hits != 1 {
		t.Errorf("allowlist fetched %d times in three requests, want 1", hits)
	}

	// The exact plugin store URL is allowed before the allowlist loads.
	fake.allowlist = `[]`
	resetCORSCache()
	response := h.corsGet(t, pluginStoreURL)
	if response.Code != http.StatusOK {
		t.Errorf("plugin store: status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestCORSProxyAllowlistFailure(t *testing.T) {
	h, fake := newCORSHarness(t)

	fake.allowErr = io.ErrUnexpectedEOF
	response := h.corsGet(t, "https://github.com/user/repo1")
	if response.Code != http.StatusForbidden || jsonError(t, response)["message"] != "Unable to verify allowlist" {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	fake.allowErr = nil
	fake.allowlist = `not json`
	resetCORSCache()
	response = h.corsGet(t, "https://github.com/user/repo1")
	if response.Code != http.StatusForbidden || jsonError(t, response)["message"] != "Unable to verify allowlist" {
		t.Fatalf("invalid allowlist: status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestCORSProxyMethods(t *testing.T) {
	h, fake := newCORSHarness(t)
	target := "https://github.com/user/repo1"

	if response := h.corsGetBody(t, target, map[string]any{"method": "GET"}); response.Code != http.StatusOK {
		t.Errorf("GET: %d", response.Code)
	}
	if response := h.corsGetBody(t, target, map[string]any{"method": "get"}); response.Code != http.StatusOK {
		t.Errorf("lowercase get: %d", response.Code)
	}
	if got := fake.lastRequest().Method; got != http.MethodGet {
		t.Errorf("forwarded method = %q", got)
	}

	if response := h.corsGetBody(t, target, map[string]any{"method": "HEAD"}); response.Code != http.StatusOK {
		t.Errorf("HEAD: %d", response.Code)
	}

	for _, method := range []string{"POST", "PUT"} {
		response := h.corsGetBody(t, target, map[string]any{"method": method})
		if response.Code != http.StatusMethodNotAllowed || jsonError(t, response)["error"] != "Method not allowed" {
			t.Errorf("%s: %d %q", method, response.Code, response.Body.String())
		}
	}

	for _, method := range []any{[]string{"POST"}, map[string]any{"toString": "POST"}, 5} {
		response := h.corsGetBody(t, target, map[string]any{"method": method})
		if response.Code != http.StatusBadRequest || jsonError(t, response)["error"] != "Invalid method parameter" {
			t.Errorf("method %v: %d %q", method, response.Code, response.Body.String())
		}
	}
}

func TestCORSProxyGitHubAuth(t *testing.T) {
	h, fake := newCORSHarness(t)
	h.config.GithubToken = "test-github-token"

	if response := h.corsGet(t, "https://api.github.com/repos/user/repo1"); response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	headers := fake.lastRequest().Header
	if got := headers.Get("Authorization"); got != "Bearer test-github-token" {
		t.Errorf("Authorization = %q", got)
	}
	if got := headers.Get("User-Agent"); got != "Actual-Budget-Plugin-System" {
		t.Errorf("User-Agent = %q", got)
	}

	h.config.GithubToken = ""
	if response := h.corsGet(t, "https://api.github.com/repos/user/repo1"); response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if got := fake.lastRequest().Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
}

func TestCORSProxyResponses(t *testing.T) {
	h, fake := newCORSHarness(t)

	fake.responses["https://github.com/user/repo1/manifest.json"] = proxyResponse(200, "application/json", `{"name":"test","version":"1.0.0"}`)
	response := h.corsGet(t, "https://github.com/user/repo1/manifest.json")
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("json: %d %q", response.Code, response.Header().Get("Content-Type"))
	}
	if body := jsonError(t, response); body["name"] != "test" {
		t.Errorf("json body = %v", body)
	}

	fake.responses["https://github.com/user/repo1/readme.txt"] = proxyResponse(200, "text/plain", "Hello, world!")
	response = h.corsGet(t, "https://github.com/user/repo1/readme.txt")
	if response.Code != http.StatusOK || response.Body.String() != "Hello, world!" {
		t.Fatalf("text: %d %q", response.Code, response.Body.String())
	}

	fake.responses["https://github.com/user/repo1/file.bin"] = proxyResponse(200, "application/octet-stream", string([]byte{1, 2, 3, 4, 5}))
	response = h.corsGet(t, "https://github.com/user/repo1/file.bin")
	if response.Code != http.StatusOK {
		t.Fatalf("binary: %d", response.Code)
	}
	body := jsonError(t, response)
	if body["isBinary"] != true || body["contentType"] != "application/octet-stream" {
		t.Errorf("binary body = %v", body)
	}
	data := body["data"].([]any)
	if len(data) != 5 || data[0].(float64) != 1 || data[4].(float64) != 5 {
		t.Errorf("binary data = %v", data)
	}

	fake.responses["https://github.com/user/repo1/invalid.json"] = proxyResponse(200, "text/plain", "not valid json")
	response = h.corsGet(t, "https://github.com/user/repo1/invalid.json")
	if response.Code != http.StatusOK || response.Body.String() != "not valid json" {
		t.Errorf("invalid json: %d %q", response.Code, response.Body.String())
	}

	fake.responses["https://github.com/user/repo1/package.json"] = proxyResponse(200, "text/plain", `{"test":true}`)
	response = h.corsGet(t, "https://github.com/user/repo1/package.json")
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
		t.Errorf("url-detected json: %d %q", response.Code, response.Header().Get("Content-Type"))
	}

	fake.errors["https://github.com/user/repo1/network-error"] = io.ErrUnexpectedEOF
	response = h.corsGet(t, "https://github.com/user/repo1/network-error")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("error status = %d", response.Code)
	}
	if body := jsonError(t, response); body["error"] != "Error proxying request" || body["details"] == "" {
		t.Errorf("error body = %v", body)
	}
}

func TestCORSProxyStripsSessionHeaders(t *testing.T) {
	h, fake := newCORSHarness(t)

	request := httptest.NewRequest(http.MethodGet, "/cors-proxy/?url="+url.QueryEscape("https://github.com/user/repo1"), nil)
	request.Header.Set("X-Actual-Token", h.userToken)
	request.Header.Set("Cookie", "secret=1")
	request.Header.Set("X-Custom", "kept")
	if response := h.do(request); response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}

	headers := fake.lastRequest().Header
	if headers.Get("X-Actual-Token") != "" || headers.Get("Cookie") != "" {
		t.Errorf("forwarded sensitive headers: %v", headers)
	}
	if headers.Get("X-Custom") != "kept" {
		t.Errorf("custom header not forwarded: %v", headers)
	}
}

func jsonError(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	body := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", response.Body.String(), err)
	}
	return body
}
