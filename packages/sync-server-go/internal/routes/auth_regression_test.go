package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminUpdateChecksEffectiveRole(t *testing.T) {
	h := newHarness(t)
	response := h.patchJSON(t, "/admin/users", map[string]any{
		"id": h.user.Id, "userName": h.user.Email(), "ROLE": "ADMIN",
	}, h.userToken)
	if response.Code != http.StatusForbidden || h.mustUser(t, h.user.Id).GetString("role") != "BASIC" {
		t.Fatalf("role update = %d %s", response.Code, response.Body)
	}
}

func TestAdminCreateChecksEffectiveRole(t *testing.T) {
	h := newHarness(t)
	admin := h.createUser(t, "other-admin@example.com", "admin-password", "ADMIN", true, false)
	request := httptest.NewRequest(http.MethodPost, "/admin/users",
		strings.NewReader(`{"userName":"new@example.com","role":"BASIC","Role":"ADMIN","enabled":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Actual-Token", h.token(t, admin))
	response := h.do(request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("create = %d %s", response.Code, response.Body)
	}
	if _, err := h.app.FindAuthRecordByEmail("users", "new@example.com"); err == nil {
		t.Fatal("denied user was created")
	}
}

func TestAdminSelfEditRejectsCredentialChanges(t *testing.T) {
	h := newHarness(t)
	for _, test := range []struct {
		name, email string
		password    *string
		oauthOnly   bool
	}{
		{"email", "changed@example.com", nil, false},
		{"password", "", ptr("changed-password"), false},
		{"oauth password", "", ptr("changed-password"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			user := h.createUser(t, "profile-"+strings.ReplaceAll(test.name, " ", "-")+"@example.com", "test-password", "BASIC", true, false)
			if test.oauthOnly {
				user = h.createOAuthUser(t, "oauth-only@example.com", "BASIC")
			}
			email := test.email
			if email == "" {
				email = user.Email()
			}
			body := map[string]any{"id": user.Id, "userName": email, "role": "BASIC", "enabled": true}
			if test.password != nil {
				body["password"] = *test.password
			}
			response := h.patchJSON(t, "/admin/users", body, h.token(t, user))
			if response.Code != http.StatusForbidden {
				t.Fatalf("self edit = %d %s", response.Code, response.Body)
			}
			saved := h.mustUser(t, user.Id)
			if saved.Email() != user.Email() || saved.GetBool("password_set") == test.oauthOnly || saved.ValidatePassword("changed-password") {
				t.Fatal("self edit changed credentials")
			}
		})
	}
}

func TestAdminUpdatePreservesOmittedFields(t *testing.T) {
	h := newHarness(t)
	user := h.mustUser(t, h.user.Id)
	user.Set("name", "Original name")
	if err := h.app.Save(user); err != nil {
		t.Fatal(err)
	}
	response := h.patchJSON(t, "/admin/users", map[string]any{
		"id": user.Id, "userName": user.Email(), "role": "BASIC",
	}, h.adminToken)
	if response.Code != http.StatusOK {
		t.Fatalf("update = %d %s", response.Code, response.Body)
	}
	saved := h.mustUser(t, user.Id)
	if !saved.GetBool("enabled") || saved.GetString("name") != "Original name" {
		t.Fatalf("enabled = %v, name = %q", saved.GetBool("enabled"), saved.GetString("name"))
	}
}

func TestRevokedTokenRejectedByPocketBase(t *testing.T) {
	h := newHarness(t)
	path := "/api/collections/users/records/" + h.user.Id
	if response := h.pbRequest(t, http.MethodGet, path, nil, h.userToken); response.Code != http.StatusOK {
		t.Fatalf("before logout = %d %s", response.Code, response.Body)
	}
	if response := h.postJSON(t, "/account/logout", map[string]string{"token": h.userToken}, ""); response.Code != http.StatusOK {
		t.Fatalf("logout = %d %s", response.Code, response.Body)
	}
	if response := h.pbRequest(t, http.MethodGet, path, nil, "Bearer "+h.userToken); response.Code != http.StatusNotFound {
		t.Errorf("after logout = %d %s", response.Code, response.Body)
	}
	if response := h.pbRequest(t, http.MethodPost, "/api/collections/users/auth-refresh", nil, h.userToken); response.Code != http.StatusUnauthorized {
		t.Errorf("refresh = %d %s", response.Code, response.Body)
	}
}

func TestConditionalLoginFailuresCountAgainstRateLimit(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 6; i++ {
		request := httptest.NewRequest(http.MethodPost, "/account/login",
			strings.NewReader(`{"email":"user@example.com","password":"wrong-password"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("If-None-Match", "*")
		want := http.StatusBadRequest
		if i == 5 {
			want = http.StatusTooManyRequests
		}
		if response := h.do(request); response.Code != want {
			t.Errorf("attempt %d = %d, want %d", i+1, response.Code, want)
		}
	}
}

func TestDisabledCollectionLoginMethods(t *testing.T) {
	h := newHarness(t)
	h.configureOAuth(t, newFakeOIDC(t))
	users, err := h.app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	users.PasswordAuth.Enabled = false
	users.OAuth2.Enabled = false
	if err := h.app.Save(users); err != nil {
		t.Fatal(err)
	}
	if methods := jsonBody(t, h.getJSON("/account/login-methods", ""))["methods"].([]any); len(methods) != 0 {
		t.Errorf("advertised disabled methods: %v", methods)
	}
	if response := h.postJSON(t, "/account/login", map[string]string{
		"email": h.user.Email(), "password": "user-password",
	}, ""); response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "invalid-password" {
		t.Errorf("disabled password login = %d %s", response.Code, response.Body)
	}
}

func TestOAuthRedirectRequiresHTTP(t *testing.T) {
	for _, redirect := range []string{"ftp://localhost", "//localhost/path"} {
		if isValidRedirectURL(redirect, "https://actual.example.com") {
			t.Errorf("accepted redirect %q", redirect)
		}
	}
}

func TestGlobalRateLimitAlsoAppliesToLogin(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 500; i++ {
		h.getJSON("/account/login-methods", "")
	}
	response := h.postJSON(t, "/account/login", map[string]string{
		"email": h.user.Email(), "password": "user-password",
	}, "")
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("login = %d, want 429", response.Code)
	}
}
