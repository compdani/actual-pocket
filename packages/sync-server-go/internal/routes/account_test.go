package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

func TestNeedsBootstrap(t *testing.T) {
	empty := newEmptyHarness(t)
	response := empty.getJSON("/account/needs-bootstrap", "")
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}

	body := jsonBody(t, response)
	if body["status"] != "ok" {
		t.Errorf("status = %v", body["status"])
	}
	data := body["data"].(map[string]any)
	if data["bootstrapped"] != false {
		t.Errorf("bootstrapped = %v, want false", data["bootstrapped"])
	}
	if data["loginMethod"] != "password" {
		t.Errorf("loginMethod = %v", data["loginMethod"])
	}
	if data["multiuser"] != true {
		t.Errorf("multiuser = %v, want true", data["multiuser"])
	}
	methods := data["availableLoginMethods"].([]any)
	if len(methods) != 1 || methods[0].(map[string]any)["method"] != "password" {
		t.Errorf("availableLoginMethods = %v, want only password", methods)
	}

	seeded := newHarness(t)
	body = jsonBody(t, seeded.getJSON("/account/needs-bootstrap", ""))
	if body["data"].(map[string]any)["bootstrapped"] != true {
		t.Error("bootstrapped = false on a seeded server")
	}
}

func TestBootstrap(t *testing.T) {
	h := newEmptyHarness(t)

	response := h.postJSON(t, "/account/bootstrap", map[string]string{
		"email":    "owner@example.com",
		"password": "correct-horse",
	}, "")
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if got, want := response.Body.String(), `{"status":"ok","data":{}}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}

	record, err := h.app.FindAuthRecordByEmail("users", "owner@example.com")
	if err != nil {
		t.Fatalf("user was not created: %v", err)
	}
	if record.GetString("role") != "ADMIN" || !record.GetBool("owner") || !record.GetBool("enabled") || !record.GetBool("verified") {
		t.Errorf("created user = role %q owner %v enabled %v verified %v",
			record.GetString("role"), record.GetBool("owner"), record.GetBool("enabled"), record.GetBool("verified"))
	}
	if !record.ValidatePassword("correct-horse") {
		t.Error("password was not set")
	}

	// A second bootstrap is refused.
	response = h.postJSON(t, "/account/bootstrap", map[string]string{
		"email":    "second@example.com",
		"password": "correct-horse",
	}, "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("second bootstrap status = %d", response.Code)
	}
	if got := jsonBody(t, response)["reason"]; got != "already-bootstrapped" {
		t.Errorf("reason = %v", got)
	}
}

func TestBootstrapValidation(t *testing.T) {
	cases := []struct {
		name string
		body map[string]string
		want string
	}{
		{"short password", map[string]string{"email": "a@example.com", "password": "short"}, "invalid-password"},
		{"missing password", map[string]string{"email": "a@example.com"}, "invalid-password"},
		{"bad email", map[string]string{"email": "not-an-email", "password": "correct-horse"}, "invalid-email"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newEmptyHarness(t)
			response := h.postJSON(t, "/account/bootstrap", testCase.body, "")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
			}
			if got := jsonBody(t, response)["reason"]; got != testCase.want {
				t.Errorf("reason = %v, want %q", got, testCase.want)
			}
		})
	}
}

// TestBootstrapRace boots with several concurrent requests: exactly one may
// create the owner.
func TestBootstrapRace(t *testing.T) {
	h := newEmptyHarness(t)

	const attempts = 6
	var wg sync.WaitGroup
	results := make(chan int, attempts)

	for index := 0; index < attempts; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			response := h.postJSON(t, "/account/bootstrap", map[string]string{
				"email":    "owner@example.com",
				"password": "correct-horse",
			}, "")
			results <- response.Code
		}(index)
	}
	wg.Wait()
	close(results)

	successes := 0
	for status := range results {
		switch status {
		case 200:
			successes++
		case http.StatusBadRequest:
			// already-bootstrapped
		case http.StatusTooManyRequests:
			// The auth limiter counts the losing attempts, as Node's does.
		default:
			t.Errorf("unexpected status %d", status)
		}
	}

	if successes != 1 {
		t.Fatalf("%d bootstraps succeeded, want exactly 1", successes)
	}

	count, err := h.store.CountUsers()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("%d users, want 1", count)
	}
}

func TestLoginMethods(t *testing.T) {
	h := newHarness(t)

	response := h.getJSON("/account/login-methods", "")
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}

	body := jsonBody(t, response)
	methods := body["methods"].([]any)
	if len(methods) != 1 {
		t.Fatalf("methods = %v, want only password", methods)
	}
	password := methods[0].(map[string]any)
	if password["method"] != "password" || password["displayName"] != "Email & password" || password["active"] != true {
		t.Errorf("password method = %v", password)
	}

	// A provider configured in the admin UI shows up as an openid method.
	users, err := h.app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	users.OAuth2.Enabled = true
	users.OAuth2.Providers = append(users.OAuth2.Providers, core.OAuth2ProviderConfig{
		Name:         "oidc",
		DisplayName:  "Company SSO",
		ClientId:     "id",
		ClientSecret: "secret",
	})
	if err := h.app.Save(users); err != nil {
		t.Fatal(err)
	}

	body = jsonBody(t, h.getJSON("/account/login-methods", ""))
	methods = body["methods"].([]any)
	if len(methods) != 2 {
		t.Fatalf("methods = %v, want password + openid", methods)
	}
	openid := methods[1].(map[string]any)
	if openid["method"] != "openid" || openid["provider"] != "oidc" || openid["displayName"] != "Company SSO" || openid["active"] != true {
		t.Errorf("openid method = %v", openid)
	}
}

func TestLoginPassword(t *testing.T) {
	h := newHarness(t)

	response := h.postJSON(t, "/account/login", map[string]string{
		"loginMethod": "password",
		"email":       "user@example.com",
		"password":    "user-password",
	}, "")
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	token, _ := jsonBody(t, response)["data"].(map[string]any)["token"].(string)
	if token == "" {
		t.Fatal("no token in the response")
	}

	record, err := h.app.FindAuthRecordByToken(token, core.TokenTypeAuth)
	if err != nil {
		t.Fatalf("returned token does not resolve: %v", err)
	}
	if record.Id != h.user.Id {
		t.Errorf("token belongs to %s, want %s", record.Id, h.user.Id)
	}
}

func TestLoginFailures(t *testing.T) {
	cases := []struct {
		name string
		body map[string]string
		want string
	}{
		{
			name: "unknown email",
			body: map[string]string{"email": "nobody@example.com", "password": "user-password"},
			want: "invalid-password",
		},
		{
			name: "wrong password",
			body: map[string]string{"email": "user@example.com", "password": "wrong-password"},
			want: "invalid-password",
		},
		{
			name: "disabled user",
			body: map[string]string{"email": "disabled@example.com", "password": "disabled-password"},
			want: "user-disabled",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t)
			response := h.postJSON(t, "/account/login", testCase.body, "")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
			}
			if got := jsonBody(t, response)["reason"]; got != testCase.want {
				t.Errorf("reason = %v, want %q", got, testCase.want)
			}
		})
	}
}

func TestLogoutRevokesOnlyThatToken(t *testing.T) {
	h := newHarness(t)

	login := h.postJSON(t, "/account/login", map[string]string{
		"email":    "user@example.com",
		"password": "user-password",
	}, "")
	first := jsonBody(t, login)["data"].(map[string]any)["token"].(string)

	// A second session for the same user stays valid. Tokens minted in the same
	// second are identical (same claims and exp), so sign this one with a
	// different expiry.
	second := h.tokenWithDuration(t, h.user, 48*time.Hour)

	response := h.postJSON(t, "/account/logout", map[string]string{"token": first}, "")
	if response.Code != 200 {
		t.Fatalf("logout status = %d, body = %q", response.Code, response.Body.String())
	}

	revoked := h.getJSON("/account/validate", first)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d, want 401", revoked.Code)
	}
	if got, want := revoked.Body.String(), `{"status":"error","reason":"unauthorized","details":"token-not-found"}`; got != want {
		t.Errorf("revoked body = %q, want %q", got, want)
	}

	if other := h.getJSON("/account/validate", second); other.Code != 200 {
		t.Errorf("the other session was revoked too: %d", other.Code)
	}
}

func TestLogoutErrors(t *testing.T) {
	h := newHarness(t)

	missing := h.postJSON(t, "/account/logout", map[string]string{}, "")
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing token status = %d", missing.Code)
	}
	if got := jsonBody(t, missing)["reason"]; got != "invalid-token" {
		t.Errorf("reason = %v", got)
	}

	invalid := h.postJSON(t, "/account/logout", map[string]string{"token": "not-a-token"}, "")
	if invalid.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d", invalid.Code)
	}
}

func TestChangePassword(t *testing.T) {
	h := newHarness(t)

	oldToken := h.token(t, h.user)

	response := h.postJSON(t, "/account/change-password", map[string]string{
		"currentPassword": "user-password",
		"password":        "new-user-password",
	}, oldToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	newToken := jsonBody(t, response)["data"].(map[string]any)["token"].(string)
	if newToken == "" {
		t.Fatal("change-password did not return a token")
	}

	// The old token is dead (tokenKey rotated)...
	if got := h.getJSON("/account/validate", oldToken); got.Code != http.StatusUnauthorized {
		t.Errorf("old token status = %d, want 401", got.Code)
	}
	// ...while the returned one keeps this device logged in.
	if got := h.getJSON("/account/validate", newToken); got.Code != 200 {
		t.Errorf("new token status = %d, body = %q", got.Code, got.Body.String())
	}

	// The new password works and the old one does not.
	record, err := h.app.FindAuthRecordByEmail("users", "user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !record.ValidatePassword("new-user-password") {
		t.Error("the new password does not work")
	}
	if record.ValidatePassword("user-password") {
		t.Error("the old password still works")
	}
}

func TestChangePasswordErrors(t *testing.T) {
	h := newHarness(t)
	token := h.token(t, h.user)

	wrong := h.postJSON(t, "/account/change-password", map[string]string{
		"currentPassword": "wrong-password",
		"password":        "new-user-password",
	}, token)
	if wrong.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", wrong.Code)
	}
	if got := jsonBody(t, wrong)["reason"]; got != "invalid-current-password" {
		t.Errorf("reason = %v", got)
	}

	short := h.postJSON(t, "/account/change-password", map[string]string{
		"currentPassword": "user-password",
		"password":        "short",
	}, token)
	if short.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", short.Code)
	}
	if got := jsonBody(t, short)["reason"]; got != "invalid-password" {
		t.Errorf("reason = %v", got)
	}

	unauthenticated := h.postJSON(t, "/account/change-password", map[string]string{
		"currentPassword": "user-password",
		"password":        "new-user-password",
	}, "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d, want 401", unauthenticated.Code)
	}
}

func TestServerPrefs(t *testing.T) {
	h := newHarness(t)

	response := h.postJSON(t, "/account/server-prefs", map[string]any{
		"prefs": map[string]any{"floatingSidebar": "true", "theme": "dark"},
	}, h.userToken)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", response.Code)
	}
	if got := jsonBody(t, response)["details"]; got != "permission-not-found" {
		t.Errorf("details = %v", got)
	}

	response = h.postJSON(t, "/account/server-prefs", map[string]any{
		"prefs": map[string]any{"floatingSidebar": "true", "theme": "dark"},
	}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("admin status = %d, body = %q", response.Code, response.Body.String())
	}

	prefs, err := h.store.ServerPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if prefs["floatingSidebar"] != "true" || prefs["theme"] != "dark" {
		t.Errorf("prefs = %v", prefs)
	}

	// Updating an existing key replaces it.
	response = h.postJSON(t, "/account/server-prefs", map[string]any{
		"prefs": map[string]any{"theme": "light"},
	}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("update status = %d", response.Code)
	}
	prefs, _ = h.store.ServerPrefs()
	if prefs["theme"] != "light" || prefs["floatingSidebar"] != "true" {
		t.Errorf("prefs after update = %v", prefs)
	}

	invalid := h.postJSON(t, "/account/server-prefs", map[string]any{}, h.adminToken)
	if invalid.Code != http.StatusBadRequest {
		t.Errorf("missing prefs status = %d, want 400", invalid.Code)
	}
}

func TestValidate(t *testing.T) {
	h := newHarness(t)

	// Set a display name and a pref first.
	h.user.Set("name", "Test User")
	if err := h.app.Save(h.user); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetServerPrefs(map[string]any{"theme": "dark"}); err != nil {
		t.Fatal(err)
	}

	response := h.getJSON("/account/validate", h.userToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	data := jsonBody(t, response)["data"].(map[string]any)
	want := map[string]any{
		"validated":   true,
		"userName":    "user@example.com",
		"displayName": "Test User",
		"userId":      h.user.Id,
		"permission":  "BASIC",
		"loginMethod": "password",
	}
	for key, value := range want {
		if data[key] != value {
			t.Errorf("%s = %v, want %v", key, data[key], value)
		}
	}
	if data["prefs"].(map[string]any)["theme"] != "dark" {
		t.Errorf("prefs = %v", data["prefs"])
	}
}

func TestTokenLookupOrder(t *testing.T) {
	h := newHarness(t)

	// Authorization: Bearer <token>
	request := httptest.NewRequest(http.MethodGet, "/account/validate", nil)
	request.Header.Set("Authorization", "Bearer "+h.userToken)
	if got := h.do(request); got.Code != 200 {
		t.Errorf("Bearer header status = %d, body = %q", got.Code, got.Body.String())
	}

	// Authorization: <token> without the prefix.
	request = httptest.NewRequest(http.MethodGet, "/account/validate", nil)
	request.Header.Set("Authorization", h.userToken)
	if got := h.do(request); got.Code != 200 {
		t.Errorf("plain Authorization status = %d, body = %q", got.Code, got.Body.String())
	}

	// Body token, with no header at all.
	response := h.postJSON(t, "/account/change-password", map[string]string{
		"token":           h.userToken,
		"currentPassword": "user-password",
		"password":        "another-password",
	}, "")
	if response.Code != 200 {
		t.Errorf("body token status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestValidateTokenStates(t *testing.T) {
	h := newHarness(t)

	// Invalid.
	invalid := h.getJSON("/account/validate", "definitely-not-a-token")
	if invalid.Code != http.StatusUnauthorized {
		t.Fatalf("invalid status = %d", invalid.Code)
	}
	if got := jsonBody(t, invalid)["details"]; got != "token-not-found" {
		t.Errorf("invalid details = %v", got)
	}

	// Expired: a valid signature with an exp in the past.
	expired := h.tokenWithDuration(t, h.user, -time.Hour)

	response := h.getJSON("/account/validate", expired)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expired status = %d", response.Code)
	}
	if got := jsonBody(t, response)["reason"]; got != "token-expired" {
		t.Errorf("expired reason = %v, want token-expired", got)
	}

	// Disabled user.
	disabled := h.getJSON("/account/validate", h.disabledToken)
	if disabled.Code != http.StatusUnauthorized {
		t.Fatalf("disabled status = %d", disabled.Code)
	}
	if got := jsonBody(t, disabled)["details"]; got != "token-not-found" {
		t.Errorf("disabled details = %v", got)
	}
}

func TestValidateWithPocketBaseToken(t *testing.T) {
	h := newHarness(t)

	// Use PocketBase's own auth endpoint, not our code, to get the token.
	pbRouter, err := apis.NewRouter(h.app)
	if err != nil {
		t.Fatalf("PocketBase router: %v", err)
	}
	mux, err := pbRouter.BuildMux()
	if err != nil {
		t.Fatalf("PocketBase mux: %v", err)
	}

	payload := strings.NewReader(`{"identity":"user@example.com","password":"user-password"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/collections/users/auth-with-password", payload)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)

	if recorder.Code != 200 {
		t.Fatalf("PocketBase auth status = %d, body = %q", recorder.Code, recorder.Body.String())
	}

	decoded := struct {
		Token string `json:"token"`
	}{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Token == "" {
		t.Fatal("PocketBase returned no token")
	}

	// And it works everywhere we accept tokens.
	if got := h.getJSON("/account/validate", decoded.Token); got.Code != 200 {
		t.Errorf("/account/validate status = %d, body = %q", got.Code, got.Body.String())
	}

	h.standardFile(t, "file-1", "group-1", "key-id", h.user.Id)
	if got := h.postSync(t, syncRequest("file-1", "group-1", "key-id", zeroSince), decoded.Token); got.Code != 200 {
		t.Errorf("/sync/sync status = %d, body = %q", got.Code, got.Body.String())
	}
}

func TestAuthRateLimit(t *testing.T) {
	h := newHarness(t)

	// Five failures use up the window...
	for attempt := 0; attempt < 5; attempt++ {
		response := h.postJSON(t, "/account/login", map[string]string{
			"email":    "user@example.com",
			"password": "wrong-password",
		}, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d status = %d", attempt, response.Code)
		}
	}

	// ...and the sixth is refused before the password is checked.
	response := h.postJSON(t, "/account/login", map[string]string{
		"email":    "user@example.com",
		"password": "user-password",
	}, "")
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", response.Code)
	}
	if got, want := response.Body.String(), `{"status":"error","reason":"too-many-requests"}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestSuccessfulLoginsDoNotCountAgainstTheLimit(t *testing.T) {
	h := newHarness(t)

	for attempt := 0; attempt < 8; attempt++ {
		response := h.postJSON(t, "/account/login", map[string]string{
			"email":    "user@example.com",
			"password": "user-password",
		}, "")
		if response.Code != 200 {
			t.Fatalf("attempt %d status = %d, body = %q", attempt, response.Code, response.Body.String())
		}
	}
}

func TestRevokedTokensArePruned(t *testing.T) {
	h := newHarness(t)

	if err := h.store.RevokeToken("expired-hash", time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := h.store.RevokeToken("live-hash", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}

	pruned, err := h.store.PruneRevokedTokens(time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Errorf("pruned %d, want 1", pruned)
	}

	revoked, err := h.store.IsTokenRevoked("expired-hash")
	if err != nil {
		t.Fatal(err)
	}
	if revoked {
		t.Error("the expired row survived")
	}

	revoked, _ = h.store.IsTokenRevoked("live-hash")
	if !revoked {
		t.Error("the live row was pruned")
	}
}

func TestMaintenanceCronPrunes(t *testing.T) {
	h := newHarness(t)

	if err := h.store.RevokeToken("expired-hash", time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}

	for _, job := range h.app.Cron().Jobs() {
		if job.Id() != "actualPruneRevoked" {
			continue
		}
		if got := job.Expression(); got != "0 * * * *" {
			t.Errorf("cron expression = %q, want hourly", got)
		}

		// Run the job the way the scheduler would.
		job.Run()

		revoked, err := h.store.IsTokenRevoked("expired-hash")
		if err != nil {
			t.Fatal(err)
		}
		if revoked {
			t.Error("the cron job did not prune the expired token")
		}
		return
	}

	t.Fatal("actualPruneRevoked is not registered")
}

// TestSyncWithAPIKey covers the budget-scoped API key path end to end.
func TestSyncWithAPIKey(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)
	h.standardFile(t, "file-2", "group-2", "key-id", h.admin.Id)

	keys, err := h.app.FindCollectionByNameOrId("actual_api_keys")
	if err != nil {
		t.Fatal(err)
	}
	key := core.NewRecord(keys)
	key.Set("user_id", h.admin.Id)
	key.Set("name", "test key")
	key.Set("key_hash", "placeholder")
	key.Set("created_at", time.Now().Unix())
	if err := h.app.Save(key); err != nil {
		t.Fatal(err)
	}

	secret := "actual_" + strings.Repeat("a", 43)
	key.Set("key_hash", store.HashToken(secret))
	if err := h.app.Save(key); err != nil {
		t.Fatal(err)
	}

	budgets, err := h.app.FindCollectionByNameOrId("actual_api_key_budgets")
	if err != nil {
		t.Fatal(err)
	}
	grant := core.NewRecord(budgets)
	grant.Set("key_id", key.Id)
	grant.Set("file_id", "file-1")
	if err := h.app.Save(grant); err != nil {
		t.Fatal(err)
	}

	// The granted budget syncs.
	if response := h.postSync(t, syncRequest("file-1", "group-1", "key-id", zeroSince), secret); response.Code != 200 {
		t.Errorf("granted budget status = %d, body = %q", response.Code, response.Body.String())
	}

	// A budget outside the grant is a 403.
	if response := h.postSync(t, syncRequest("file-2", "group-2", "key-id", zeroSince), secret); response.Code != http.StatusForbidden {
		t.Errorf("other budget status = %d, want 403", response.Code)
	}

	// /account/validate reports the api-key login method.
	response := h.getJSON("/account/validate", secret)
	if response.Code != 200 {
		t.Fatalf("validate status = %d, body = %q", response.Code, response.Body.String())
	}
	data := jsonBody(t, response)["data"].(map[string]any)
	if data["validated"] != true || data["loginMethod"] != "api-key" {
		t.Errorf("api key validate data = %v", data)
	}

	// An unknown key is a 401 with the short body.
	unknown := "actual_" + strings.Repeat("b", 43)
	if response := h.getJSON("/account/validate", unknown); response.Code != http.StatusUnauthorized {
		t.Errorf("unknown key status = %d", response.Code)
	} else if got, want := response.Body.String(), `{"status":"error","reason":"unauthorized"}`; got != want {
		t.Errorf("unknown key body = %q, want %q", got, want)
	}
}
