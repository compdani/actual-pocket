package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/auth"
)

// TestEachLoginGetsItsOwnToken is the regression test for two logins in the
// same second: PocketBase tokens only carry whole-second expiries, so without
// the issuer both logins would share one token and logging out either would
// revoke both.
func TestEachLoginGetsItsOwnToken(t *testing.T) {
	h := newHarness(t)

	tokens := make([]string, 0, 3)
	for i := range 3 {
		response := h.postJSON(t, "/account/login", map[string]string{
			"loginMethod": "password",
			"email":       "user@example.com",
			"password":    "user-password",
		}, "")
		if response.Code != http.StatusOK {
			t.Fatalf("login %d status = %d, body = %q", i, response.Code, response.Body.String())
		}

		data := jsonBody(t, response)["data"].(map[string]any)
		token, ok := data["token"].(string)
		if !ok || token == "" {
			t.Fatalf("login %d returned no token: %v", i, data)
		}
		tokens = append(tokens, token)
	}

	for i, token := range tokens {
		for j := range i {
			if token == tokens[j] {
				t.Fatalf("logins %d and %d returned the same token", j, i)
			}
		}

		if i == 0 {
			continue
		}

		prev, ok := auth.TokenExpiry(tokens[i-1])
		if !ok {
			t.Fatalf("login %d token has no exp", i-1)
		}
		current, ok := auth.TokenExpiry(token)
		if !ok {
			t.Fatalf("login %d token has no exp", i)
		}
		if current <= prev {
			t.Errorf("login %d exp = %d, want > %d", i, current, prev)
		}
	}

	// Logging out the first token must leave the others valid.
	logout := h.postJSON(t, "/account/logout", map[string]string{"token": tokens[0]}, "")
	if logout.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %q", logout.Code, logout.Body.String())
	}

	for i, token := range tokens {
		request := httptest.NewRequest(http.MethodGet, "/account/validate", nil)
		request.Header.Set("X-Actual-Token", token)
		got := h.do(request)

		if i == 0 && got.Code != http.StatusUnauthorized {
			t.Errorf("logged-out token status = %d, want 401", got.Code)
		}
		if i > 0 && got.Code != http.StatusOK {
			t.Errorf("token %d status = %d, want 200 (body %q)", i, got.Code, got.Body.String())
		}
	}
}

func TestIssuedTokensStayDistinctAcrossSeconds(t *testing.T) {
	h := newHarness(t)

	record, err := h.app.FindRecordById("users", h.user.Id)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[int64]string{}
	for i := range 5 {
		token, err := h.resolver.IssueToken(record)
		if err != nil {
			t.Fatal(err)
		}

		exp, ok := auth.TokenExpiry(token)
		if !ok {
			t.Fatalf("token %d has no exp", i)
		}
		if other, ok := seen[exp]; ok {
			t.Fatalf("tokens %d and %d share exp %d:\n%s\n%s", i, len(seen), exp, other, token)
		}
		seen[exp] = token
	}

	if got := h.resolver.PruneIssuedTokens(0); got != 0 {
		t.Errorf("PruneIssuedTokens(0) = %d, want 0", got)
	}
	// The issuer remembers one expiry per user, not one per token.
	if got := h.resolver.PruneIssuedTokens(1 << 62); got != 1 {
		t.Errorf("PruneIssuedTokens(future) = %d, want 1", got)
	}
	if got := h.resolver.PruneIssuedTokens(1 << 62); got != 0 {
		t.Errorf("second prune = %d, want 0", got)
	}
}
