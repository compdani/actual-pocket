package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
)

// fakeOIDC is a minimal OpenID Connect provider: a token endpoint and a
// userinfo endpoint. The browser's authorization request never reaches it in
// these tests; the callback is called with the code directly.
type fakeOIDC struct {
	server *httptest.Server

	mu        sync.Mutex
	sub       string
	email     string
	name      string
	verified  bool
	failToken bool
	failInfo  bool
	verifier  string
	tokenHits int
}

func newFakeOIDC(t *testing.T) *fakeOIDC {
	t.Helper()

	fake := &fakeOIDC{
		sub:      "provider-user-1",
		email:    "oauth@example.com",
		name:     "OAuth User",
		verified: true,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost:3000/openid-cb", http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.tokenHits++
		fake.verifier = r.FormValue("code_verifier")
		failToken := fake.failToken
		fake.mu.Unlock()

		if failToken {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-access-token" {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}

		fake.mu.Lock()
		body := map[string]any{
			"sub":            fake.sub,
			"email":          fake.email,
			"name":           fake.name,
			"email_verified": fake.verified,
		}
		failInfo := fake.failInfo
		fake.mu.Unlock()

		if failInfo {
			http.Error(w, "userinfo down", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)

	return fake
}

// configureOAuth enables one oidc provider on the users collection.
func (h *harness) configureOAuth(t *testing.T, fake *fakeOIDC) {
	t.Helper()

	users, err := h.app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}

	users.OAuth2.Enabled = true
	users.OAuth2.Providers = []core.OAuth2ProviderConfig{{
		Name:         "oidc",
		DisplayName:  "Fake SSO",
		ClientId:     "client-id",
		ClientSecret: "client-secret",
		AuthURL:      fake.server.URL + "/authorize",
		TokenURL:     fake.server.URL + "/token",
		UserInfoURL:  fake.server.URL + "/userinfo",
	}}
	if err := h.app.Save(users); err != nil {
		t.Fatal(err)
	}
}

// startOAuth starts a login and returns the authorization URL and its state.
func (h *harness) startOAuth(t *testing.T, returnURL string) (string, string) {
	t.Helper()

	response := h.postJSON(t, "/account/login", map[string]string{
		"loginMethod": "openid",
		"provider":    "oidc",
		"returnUrl":   returnURL,
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("start status = %d, body = %q", response.Code, response.Body.String())
	}

	authURL := jsonBody(t, response)["data"].(map[string]any)["returnUrl"].(string)
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}

	return authURL, parsed.Query().Get("state")
}

// oauthCallback calls the callback endpoint and returns the response.
func (h *harness) oauthCallback(code, state string) *httptest.ResponseRecorder {
	return h.getJSON("/openid/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), "")
}

// oauthTokenFromLocation extracts the token from a success redirect.
func oauthTokenFromLocation(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()

	location := response.Header().Get("Location")
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse location %q: %v", location, err)
	}

	token := parsed.Query().Get("token")
	if token == "" {
		t.Fatalf("no token in redirect %q", location)
	}

	return token
}

func (h *harness) validateToken(t *testing.T, token string) map[string]any {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/account/validate", nil)
	request.Header.Set("X-Actual-Token", token)
	response := h.do(request)
	if response.Code != http.StatusOK {
		t.Fatalf("validate status = %d, body = %q", response.Code, response.Body.String())
	}

	return jsonBody(t, response)["data"].(map[string]any)
}

func TestOAuthStart(t *testing.T) {
	h := newHarness(t)
	fake := newFakeOIDC(t)
	h.configureOAuth(t, fake)

	// Happy path first: successful logins do not count against the auth
	// limiter, which caps this test at five failed starts.
	authURL, state := h.startOAuth(t, "http://localhost:3000")
	if state == "" {
		t.Fatal("auth URL has no state")
	}

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Errorf("PKCE params = %v", query)
	}
	if got := query.Get("redirect_uri"); got != "http://localhost:8090/openid/callback" {
		t.Errorf("redirect_uri = %q", got)
	}
	if got := query.Get("client_id"); got != "client-id" {
		t.Errorf("client_id = %q", got)
	}

	pending, err := h.app.FindFirstRecordByFilter("actual_pending_oauth", "state = {:state}", map[string]any{"state": state})
	if err != nil {
		t.Fatal(err)
	}
	if pending.GetString("provider") != "oidc" || pending.GetString("return_url") != "http://localhost:3000" {
		t.Errorf("pending = %v", pending)
	}
	if verifier := pending.GetString("code_verifier"); verifier == "" || verifier == query.Get("code_challenge") {
		t.Errorf("code_verifier = %q, want a verifier distinct from the challenge", verifier)
	}
	expires := int64(pending.GetInt("expires_at"))
	if delta := expires - time.Now().Unix(); delta < 9*60 || delta > 11*60 {
		t.Errorf("expires_at is %ds away, want about 10m", delta)
	}

	// Invalid return URLs are rejected before anything is stored.
	for _, returnURL := range []string{"https://evil.example.com", "not-a-url", ""} {
		response := h.postJSON(t, "/account/login", map[string]string{
			"loginMethod": "openid",
			"provider":    "oidc",
			"returnUrl":   returnURL,
		}, "")
		if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "Invalid redirect URL" {
			t.Errorf("returnUrl %q: status = %d, body = %q", returnURL, response.Code, response.Body.String())
		}
	}

	// An unknown provider is refused.
	response := h.postJSON(t, "/account/login", map[string]string{
		"loginMethod": "openid",
		"provider":    "google",
		"returnUrl":   "http://localhost:3000",
	}, "")
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "provider-not-configured" {
		t.Errorf("unknown provider: status = %d, body = %q", response.Code, response.Body.String())
	}

	// Disabling OAuth2 hides every provider.
	users, err := h.app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	users.OAuth2.Enabled = false
	if err := h.app.Save(users); err != nil {
		t.Fatal(err)
	}
	response = h.postJSON(t, "/account/login", map[string]string{
		"loginMethod": "openid",
		"provider":    "oidc",
		"returnUrl":   "http://localhost:3000",
	}, "")
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "provider-not-configured" {
		t.Errorf("disabled oauth2: status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestOAuthCallbackCreatesFirstUser(t *testing.T) {
	h := newEmptyHarness(t)
	fake := newFakeOIDC(t)
	h.configureOAuth(t, fake)

	_, state := h.startOAuth(t, "http://localhost:3000")

	response := h.oauthCallback("good-code", state)
	if response.Code != http.StatusFound {
		t.Fatalf("callback status = %d, body = %q", response.Code, response.Body.String())
	}
	location := response.Header().Get("Location")
	if !strings.HasPrefix(location, "http://localhost:3000/openid-cb?token=") {
		t.Fatalf("Location = %q", location)
	}

	token := oauthTokenFromLocation(t, response)
	data := h.validateToken(t, token)
	if data["permission"] != "ADMIN" || data["loginMethod"] != "openid" {
		t.Errorf("validated = %v", data)
	}

	user, err := h.app.FindRecordById("users", data["userId"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !user.GetBool("owner") || user.GetString("email") != "oauth@example.com" || user.GetString("name") != "OAuth User" {
		t.Errorf("created user = %v", user)
	}
	if user.GetBool("password_set") {
		t.Error("first OAuth2 user has password_set = true")
	}

	link, err := h.app.FindFirstExternalAuthByExpr(dbx.HashExp{
		"collectionRef": user.Collection().Id,
		"provider":      "oidc",
		"providerId":    "provider-user-1",
	})
	if err != nil {
		t.Fatalf("external auth link: %v", err)
	}
	if link.RecordRef() != user.Id {
		t.Errorf("link record = %q, want %q", link.RecordRef(), user.Id)
	}

	// The state is single use.
	replay := h.oauthCallback("good-code", state)
	if replay.Code != http.StatusBadRequest || jsonBody(t, replay)["reason"] != "invalid-or-expired-state" {
		t.Errorf("replay status = %d, body = %q", replay.Code, replay.Body.String())
	}

	// Logging out the returned token works like any other token.
	logout := h.postJSON(t, "/account/logout", map[string]string{"token": token}, "")
	if logout.Code != http.StatusOK {
		t.Errorf("logout status = %d", logout.Code)
	}
}

func TestOAuthCallbackLinksEmailMatch(t *testing.T) {
	h := newHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.UserCreationMode = "manual"
	}, true)
	fake := newFakeOIDC(t)
	fake.email = "user@example.com"
	h.configureOAuth(t, fake)

	before, err := h.app.FindRecordById("users", h.user.Id)
	if err != nil {
		t.Fatal(err)
	}

	_, state := h.startOAuth(t, "http://localhost:3000")
	response := h.oauthCallback("good-code", state)
	if response.Code != http.StatusFound {
		t.Fatalf("callback status = %d, body = %q", response.Code, response.Body.String())
	}

	data := h.validateToken(t, oauthTokenFromLocation(t, response))
	// The user still has a password, so the profile keeps offering password
	// management even though this session came from the provider.
	if data["userId"] != h.user.Id || data["permission"] != "BASIC" || data["loginMethod"] != "password" {
		t.Errorf("validated = %v", data)
	}

	after, err := h.app.FindRecordById("users", h.user.Id)
	if err != nil {
		t.Fatal(err)
	}
	if after.GetString("email") != before.GetString("email") {
		t.Error("email match changed the user's email")
	}
	if !after.GetBool("password_set") {
		t.Error("email-matched user lost password_set")
	}

	if _, err := h.app.FindFirstExternalAuthByExpr(dbx.HashExp{
		"collectionRef": h.user.Collection().Id,
		"provider":      "oidc",
		"providerId":    "provider-user-1",
	}); err != nil {
		t.Errorf("external auth link: %v", err)
	}

	count, err := h.app.CountRecords("users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Errorf("%d users, want 4 (no auto-create)", count)
	}
}

func TestOAuthCallbackAutoCreate(t *testing.T) {
	t.Run("manual mode refuses unknown emails", func(t *testing.T) {
		h := newHarnessWithConfig(t, func(cfg *config.Config) {
			cfg.UserCreationMode = "manual"
		}, true)
		fake := newFakeOIDC(t)
		fake.email = "stranger@example.com"
		h.configureOAuth(t, fake)

		_, state := h.startOAuth(t, "http://localhost:3000")
		response := h.oauthCallback("good-code", state)
		if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "user-not-found" {
			t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
		}
	})

	t.Run("login mode creates a basic user", func(t *testing.T) {
		h := newHarnessWithConfig(t, func(cfg *config.Config) {
			cfg.UserCreationMode = "login"
		}, true)
		fake := newFakeOIDC(t)
		fake.email = "stranger@example.com"
		fake.name = "Stranger"
		h.configureOAuth(t, fake)

		_, state := h.startOAuth(t, "http://localhost:3000")
		response := h.oauthCallback("good-code", state)
		if response.Code != http.StatusFound {
			t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
		}

		data := h.validateToken(t, oauthTokenFromLocation(t, response))
		if data["permission"] != "BASIC" || data["loginMethod"] != "openid" {
			t.Errorf("validated = %v", data)
		}

		user, err := h.app.FindRecordById("users", data["userId"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if user.GetBool("owner") || user.GetBool("password_set") || user.GetString("name") != "Stranger" {
			t.Errorf("created user = %v", user)
		}
	})
}

func TestOAuthCallbackDisabledUser(t *testing.T) {
	h := newHarness(t)
	fake := newFakeOIDC(t)
	fake.email = "disabled@example.com"
	h.configureOAuth(t, fake)

	_, state := h.startOAuth(t, "http://localhost:3000")
	response := h.oauthCallback("good-code", state)
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "openid-grant-failed" {
		t.Fatalf("email match: status = %d, body = %q", response.Code, response.Body.String())
	}

	// The external-auth link path refuses a disabled user too, even when the
	// provider email belongs to nobody.
	link := core.NewExternalAuth(h.app)
	link.SetCollectionRef(h.disabled.Collection().Id)
	link.SetRecordRef(h.disabled.Id)
	link.SetProvider("oidc")
	link.SetProviderId("disabled-link")
	if err := h.app.Save(link); err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	fake.sub = "disabled-link"
	fake.email = "not-a-user@example.com"
	fake.mu.Unlock()

	_, state = h.startOAuth(t, "http://localhost:3000")
	response = h.oauthCallback("good-code", state)
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "openid-grant-failed" {
		t.Fatalf("external auth: status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestOAuthCallbackErrors(t *testing.T) {
	h := newHarness(t)
	fake := newFakeOIDC(t)
	h.configureOAuth(t, fake)

	response := h.getJSON("/openid/callback?state=abc", "")
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "missing-authorization-code" {
		t.Errorf("missing code: %d %q", response.Code, response.Body.String())
	}

	response = h.getJSON("/openid/callback?code=abc", "")
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "missing-state" {
		t.Errorf("missing state: %d %q", response.Code, response.Body.String())
	}

	response = h.oauthCallback("abc", "unknown")
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "invalid-or-expired-state" {
		t.Errorf("unknown state: %d %q", response.Code, response.Body.String())
	}

	// Disabling OAuth2 after the start blocks the callback too.
	_, state := h.startOAuth(t, "http://localhost:3000")
	users, err := h.app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	users.OAuth2.Enabled = false
	if err := h.app.Save(users); err != nil {
		t.Fatal(err)
	}
	response = h.oauthCallback("good-code", state)
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "openid-not-configured" {
		t.Errorf("disabled oauth2 callback: %d %q", response.Code, response.Body.String())
	}
	h.configureOAuth(t, fake)

	// A row that is past its expiry is refused and cleaned up.
	_, state = h.startOAuth(t, "http://localhost:3000")
	pending, err := h.app.FindFirstRecordByFilter("actual_pending_oauth", "state = {:state}", map[string]any{"state": state})
	if err != nil {
		t.Fatal(err)
	}
	pending.Set("expires_at", time.Now().Add(-time.Minute).Unix())
	if err := h.app.Save(pending); err != nil {
		t.Fatal(err)
	}
	response = h.oauthCallback("good-code", state)
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "invalid-or-expired-state" {
		t.Errorf("expired state: %d %q", response.Code, response.Body.String())
	}
	if _, err := h.app.FindFirstRecordByFilter("actual_pending_oauth", "state = {:state}", map[string]any{"state": state}); err == nil {
		t.Error("expired pending row was not deleted")
	}

	// Token exchange failures are a failed grant.
	_, state = h.startOAuth(t, "http://localhost:3000")
	fake.mu.Lock()
	fake.failToken = true
	fake.mu.Unlock()
	response = h.oauthCallback("good-code", state)
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "openid-grant-failed" {
		t.Errorf("token failure: %d %q", response.Code, response.Body.String())
	}

	// Userinfo failures are a failed grant too.
	_, state = h.startOAuth(t, "http://localhost:3000")
	fake.mu.Lock()
	fake.failToken = false
	fake.failInfo = true
	fake.mu.Unlock()
	response = h.oauthCallback("good-code", state)
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "openid-grant-failed" {
		t.Errorf("userinfo failure: %d %q", response.Code, response.Body.String())
	}
}

func TestOAuthRemovedEndpoints(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/openid/enable", "/openid/disable", "/openid/config"} {
		response := h.postJSON(t, path, map[string]any{}, "")
		if response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, response.Code)
		}
	}
}

func TestIsValidRedirectURL(t *testing.T) {
	const appURL = "https://app.example.com"

	cases := []struct {
		url  string
		want bool
	}{
		{"https://app.example.com", true},
		{"https://app.example.com:3001/some/path", true},
		{"http://app.example.com", true},
		{"http://localhost:3000", true},
		{"https://localhost", true},
		{"https://evil.example.com", false},
		{"https://app.example.com.evil.com", false},
		{"not-a-url", false},
		{"", false},
	}
	for _, testCase := range cases {
		if got := isValidRedirectURL(testCase.url, appURL); got != testCase.want {
			t.Errorf("isValidRedirectURL(%q) = %v, want %v", testCase.url, got, testCase.want)
		}
	}

	if isValidRedirectURL("https://app.example.com", "") {
		t.Error("empty app URL accepted a redirect")
	}
}

func TestOAuthStateCannotBeConsumedConcurrently(t *testing.T) {
	h := newHarness(t)
	fake := newFakeOIDC(t)
	fake.email = h.user.Email()
	h.configureOAuth(t, fake)
	_, state := h.startOAuth(t, "http://localhost:3000")

	// Pause the old read-then-delete implementation after it reads the state,
	// so the second request can try to consume the same row.
	firstDelete := make(chan struct{})
	continueDelete := make(chan struct{})
	var once sync.Once
	h.app.OnRecordDelete("actual_pending_oauth").BindFunc(func(e *core.RecordEvent) error {
		first := false
		once.Do(func() {
			first = true
			close(firstDelete)
		})
		if first {
			<-continueDelete
		}
		return e.Next()
	})

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- h.oauthCallback("good-code", state)
	}()
	var first, second *httptest.ResponseRecorder
	select {
	case <-firstDelete:
		second = h.oauthCallback("good-code", state)
		close(continueDelete)
		first = <-firstDone
	case first = <-firstDone:
		close(continueDelete)
		second = h.oauthCallback("good-code", state)
	}
	if first.Code == http.StatusFound {
		first, second = second, first
	}
	if first.Code != http.StatusBadRequest || jsonBody(t, first)["reason"] != "invalid-or-expired-state" ||
		second.Code != http.StatusFound || fake.tokenHits != 1 {
		t.Fatalf("callbacks = %d %s and %d, token exchanges = %d", first.Code, first.Body, second.Code, fake.tokenHits)
	}
}
