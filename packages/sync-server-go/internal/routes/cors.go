package routes

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/ratelimit"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/ssrf"
)

// pluginStoreURL is always allowed, even before the allowlist loads.
const pluginStoreURL = "https://raw.githubusercontent.com/actualbudget/plugin-store/refs/heads/main/plugins.json"

const (
	corsAllowlistTTL     = 5 * time.Minute
	corsMaxResponseBytes = 10 << 20
)

var errCORSRedirectDenied = errors.New("proxy redirect destination is not allowed")

// Variables so tests can point the proxy at a fake allowlist and transport.
var (
	corsAllowlistURL = pluginStoreURL
	corsProxyClient  = &http.Client{
		Transport: ssrf.NewTransport(ssrf.Options{}),
		Timeout:   60 * time.Second,
	}
	corsProxyRateLimit = 25
)

var corsCache struct {
	sync.Mutex
	repos     []string
	fetchedAt time.Time
}

func resetCORSCache() {
	corsCache.Lock()
	corsCache.repos = nil
	corsCache.fetchedAt = time.Time{}
	corsCache.Unlock()
}

type corsErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
	Details string `json:"details,omitempty"`
}

type corsProxyRequest struct {
	Method  any            `json:"method"`
	Headers map[string]any `json:"headers"`
}

func registerCORSProxy(actual *router.RouterGroup[*core.RequestEvent], opts Options) {
	if !opts.Config.CorsProxyEnabled {
		return
	}

	group := actual.Group("/cors-proxy")

	limiter := ratelimit.New(ratelimit.Config{
		Limit:  corsProxyRateLimit,
		Window: time.Minute,
	})
	limiter.Identity(func(r *http.Request) string {
		return httpx.ClientIP(r, opts.Config.TrustedProxies)
	})
	group.Bind(limiter.Handler())

	// A pattern ending in "/" matches the whole subtree, like Express's
	// app.use('/cors-proxy').
	group.OPTIONS("/", opts.corsProxy)
	group.GET("/", opts.corsProxy)
}

func (o Options) corsProxy(e *core.RequestEvent) error {
	header := e.Response.Header()
	if e.Request.Method == http.MethodOptions {
		setCORSHeaders(header)
		e.Response.WriteHeader(http.StatusNoContent)
		return nil
	}

	target := e.Request.URL.Query().Get("url")
	if target == "" {
		return httpx.JSON(e, http.StatusBadRequest, corsErrorResponse{Error: "Missing url parameter"})
	}

	if o.Auth.ResolveSession(e, "") == nil {
		return nil
	}

	parsed, err := url.Parse(target)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return httpx.JSON(e, http.StatusBadRequest, corsErrorResponse{Error: "Invalid url parameter"})
	}

	repos, err := corsAllowlist()
	if err != nil {
		o.App.Logger().Error("failed to fetch plugin allowlist", "error", err)
		return httpx.JSON(e, http.StatusForbidden, corsErrorResponse{
			Error:   "URL not allowed",
			Message: "Unable to verify allowlist",
		})
	}

	if !corsURLAllowed(target, repos) {
		return httpx.JSON(e, http.StatusForbidden, corsErrorResponse{
			Error:   "URL not allowed",
			Message: "Only allowlisted plugin repositories are allowed (localhost only in development)",
		})
	}

	body := corsProxyRequest{}
	if err := bindJSON(e, &body); err != nil || e.Written() {
		return err
	}

	method := http.MethodGet
	if body.Method != nil {
		value, ok := body.Method.(string)
		if !ok {
			return httpx.JSON(e, http.StatusBadRequest, corsErrorResponse{Error: "Invalid method parameter"})
		}
		method = strings.ToUpper(value)
	}
	if method != http.MethodGet && method != http.MethodHead {
		return httpx.JSON(e, http.StatusMethodNotAllowed, corsErrorResponse{Error: "Method not allowed"})
	}

	request, err := http.NewRequestWithContext(e.Request.Context(), method, parsed.String(), nil)
	if err != nil {
		return httpx.JSON(e, http.StatusInternalServerError, corsErrorResponse{
			Error:   "Error proxying request",
			Details: err.Error(),
		})
	}

	request.Host = parsed.Host
	copyProxyHeaders(request.Header, e.Request.Header)
	for name, value := range body.Headers {
		request.Header.Set(name, fmt.Sprint(value))
	}
	// Strip client credentials after merging the inbound and supplied headers.
	for _, name := range []string{"Authorization", "X-Actual-Token", "Cookie", "Cookie2", "Content-Length"} {
		request.Header.Del(name)
	}

	if o.Config.GithubToken != "" && isGitHubURL(parsed) {
		request.Header.Set("Authorization", "Bearer "+o.Config.GithubToken)
		request.Header.Set("User-Agent", "Actual-Budget-Plugin-System")
	}

	client := *corsProxyClient
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !corsURLAllowed(next.URL.String(), repos) {
			return errCORSRedirectDenied
		}
		if !strings.EqualFold(next.URL.Host, via[0].URL.Host) || !isGitHubURL(next.URL) {
			next.Header.Del("Authorization")
		}
		return nil
	}

	response, err := client.Do(request)
	if errors.Is(err, errCORSRedirectDenied) {
		return httpx.JSON(e, http.StatusBadGateway, corsErrorResponse{Error: errCORSRedirectDenied.Error()})
	}
	if err != nil {
		return httpx.JSON(e, http.StatusInternalServerError, corsErrorResponse{
			Error:   "Error proxying request",
			Details: err.Error(),
		})
	}
	defer response.Body.Close()

	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	setCORSHeaders(header)
	payload, err := io.ReadAll(io.LimitReader(response.Body, corsMaxResponseBytes+1))
	if err != nil {
		return httpx.JSON(e, http.StatusBadGateway, corsErrorResponse{Error: "Could not read proxy response"})
	}
	if len(payload) > corsMaxResponseBytes {
		return httpx.JSON(e, http.StatusBadGateway, corsErrorResponse{Error: "Proxy response exceeds 10 MiB"})
	}

	lowerTarget := strings.ToLower(parsed.String())
	likelyJSON := strings.Contains(contentType, "application/json") ||
		strings.Contains(lowerTarget, ".json") ||
		strings.Contains(lowerTarget, "/manifest") ||
		strings.Contains(lowerTarget, "manifest.json") ||
		strings.Contains(lowerTarget, "package.json")

	if likelyJSON {
		var decoded any
		if err := json.Unmarshal(payload, &decoded); err == nil {
			return e.Blob(response.StatusCode, httpx.ContentTypeJSON, mustJSON(decoded))
		}
	}
	if likelyJSON || strings.Contains(contentType, "text/") {
		return e.Blob(response.StatusCode, contentType, payload)
	}

	data := make([]int, len(payload))
	for i, value := range payload {
		data[i] = int(value)
	}
	return httpx.JSON(e, response.StatusCode, struct {
		Data        []int  `json:"data"`
		ContentType string `json:"contentType"`
		IsBinary    bool   `json:"isBinary"`
	}{Data: data, ContentType: contentType, IsBinary: true})
}

func setCORSHeaders(header http.Header) {
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("Access-Control-Allow-Methods", "GET,HEAD,OPTIONS")
	header.Set("Access-Control-Allow-Headers", "Content-Type, X-Actual-Token")
	header.Set("Access-Control-Max-Age", "600")
}

func copyProxyHeaders(dst, src http.Header) {
	for name, values := range src {
		switch http.CanonicalHeaderKey(name) {
		case "Host", "Content-Length", "Cookie", "Cookie2", "X-Actual-Token":
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

func isGitHubURL(parsed *url.URL) bool {
	if parsed.Scheme != "https" {
		return false
	}
	switch parsed.Hostname() {
	case "api.github.com", "raw.githubusercontent.com":
		return true
	case "github.com":
		return strings.Contains(parsed.Path, "/releases/")
	default:
		return false
	}
}

// corsURLAllowed is app-cors-proxy.js's isUrlAllowed, minus the session and
// method checks.
func corsURLAllowed(target string, repos []string) bool {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return false
	}
	escapedPath := strings.ToLower(parsed.EscapedPath())
	if strings.Contains(escapedPath, "%2e") || strings.Contains(escapedPath, "%2f") ||
		strings.Contains(escapedPath, "%25") || strings.Contains(parsed.Path, "\\") {
		return false
	}
	for segment := range strings.SplitSeq(parsed.Path, "/") {
		if segment == ".." {
			return false
		}
	}

	if ssrf.IsBlockedHost(parsed.Hostname(), ssrf.Options{}) {
		return false
	}

	if target == pluginStoreURL {
		return true
	}

	for _, repo := range repos {
		repoURL, err := url.Parse(repo)
		if err != nil {
			continue
		}
		parts := strings.Split(strings.Trim(repoURL.Path, "/"), "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		owner, name := parts[0], parts[1]

		if target == repo || strings.HasPrefix(target, repo+"/") {
			return true
		}

		apiPrefix := "/repos/" + owner + "/" + name
		if parsed.Hostname() == "api.github.com" &&
			(parsed.Path == apiPrefix || strings.HasPrefix(parsed.Path, apiPrefix+"/")) {
			return true
		}
		if parsed.Hostname() == "raw.githubusercontent.com" &&
			strings.HasPrefix(parsed.Path, "/"+owner+"/"+name+"/") {
			return true
		}
		if parsed.Hostname() == "github.com" &&
			strings.HasPrefix(parsed.Path, "/"+owner+"/"+name+"/releases/") {
			return true
		}
	}

	return false
}

// corsAllowlist returns the cached plugin repository URLs, refreshing the
// cache when it is older than five minutes or empty.
func corsAllowlist() ([]string, error) {
	corsCache.Lock()
	defer corsCache.Unlock()

	if len(corsCache.repos) > 0 && time.Since(corsCache.fetchedAt) < corsAllowlistTTL {
		return corsCache.repos, nil
	}

	request, err := http.NewRequest(http.MethodGet, corsAllowlistURL, nil)
	if err != nil {
		return nil, err
	}
	client := *corsProxyClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return errCORSRedirectDenied
	}
	response, err := client.Do(request)
	if err != nil {
		corsCache.repos = nil
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		corsCache.repos = nil
		return nil, fmt.Errorf("allowlist status %d", response.StatusCode)
	}

	var plugins []struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, corsMaxResponseBytes)).Decode(&plugins); err != nil {
		corsCache.repos = nil
		return nil, err
	}

	repos := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		repos = append(repos, plugin.URL)
	}

	corsCache.repos = repos
	corsCache.fetchedAt = time.Now()

	return repos, nil
}

func mustJSON(value any) []byte {
	encoded, err := httpx.Marshal(value)
	if err != nil {
		return []byte("null")
	}
	return encoded
}
