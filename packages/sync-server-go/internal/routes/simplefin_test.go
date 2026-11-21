package routes

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
)

const simplefinFixture = `{
  "accounts": [
    {
      "id": "account-1",
      "name": "Checking",
      "org": {"name": "Bank", "domain": "bank.example", "id": "org-1"},
      "custom-field": "kept",
      "balance": "123.45",
      "balance-date": 1767225600,
      "currency": "USD",
      "transactions": [
        {"id": "tx-1", "payee": "Coffee Shop", "description": "Card purchase", "amount": "-12.34", "posted": 1767225600},
        {"id": "tx-2", "payee": "Paycheck", "description": "Salary", "amount": "1000.00", "posted": 1767312000},
        {"id": "tx-pending", "payee": "Pending Payee", "description": "Pending", "amount": "-5.00", "pending": true, "transacted_at": 1767398400}
      ]
    },
    {
      "id": "account-2",
      "org": {"name": "Bank"},
      "balance": "0.00",
      "balance-date": 1767225600,
      "currency": "USD",
      "transactions": []
    }
  ],
  "errors": []
}`

type simplefinRequest struct {
	method string
	path   string
	query  string
	auth   string
}

type fakeSimpleFIN struct {
	server *httptest.Server

	mu             sync.Mutex
	requests       []simplefinRequest
	claimBody      string
	claimStatus    int
	accountsBody   string
	accountsStatus int
	redirect       string
}

func newFakeSimpleFIN(t *testing.T) *fakeSimpleFIN {
	t.Helper()

	fake := &fakeSimpleFIN{
		claimBody:      "",
		claimStatus:    200,
		accountsBody:   simplefinFixture,
		accountsStatus: 200,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/claim", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.requests = append(fake.requests, simplefinRequest{method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization")})
		status := fake.claimStatus
		body := fake.claimBody
		fake.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.requests = append(fake.requests, simplefinRequest{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			auth:   r.Header.Get("Authorization"),
		})
		redirect := fake.redirect
		status := fake.accountsStatus
		body := fake.accountsBody
		fake.mu.Unlock()

		if redirect != "" {
			w.Header().Set("Location", redirect)
			w.WriteHeader(http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeSimpleFIN) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, request := range f.requests {
		if request.path == path {
			count++
		}
	}
	return count
}

func (f *fakeSimpleFIN) last(path string) simplefinRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.requests) - 1; i >= 0; i-- {
		if f.requests[i].path == path {
			return f.requests[i]
		}
	}
	return simplefinRequest{}
}

func (f *fakeSimpleFIN) setToken(t *testing.T) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString([]byte(f.server.URL + "/claim"))
}

func simplefinAccessKey(fake *fakeSimpleFIN, user, password string) string {
	return strings.Replace(fake.server.URL, "://", "://"+user+":"+password+"@", 1)
}

func newSimpleFINHarness(t *testing.T, allowed bool) (*harness, *fakeSimpleFIN) {
	t.Helper()

	h := newHarnessWithConfig(t, func(cfg *config.Config) {
		if allowed {
			cfg.SimpleFINAllowedNetworks = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
		}
	}, true)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)
	return h, newFakeSimpleFIN(t)
}

func TestSimpleFINStatus(t *testing.T) {
	h, fake := newSimpleFINHarness(t, true)

	response := h.postJSON(t, "/simplefin/status", nil, h.userToken)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}
	data := jsonBody(t, response)["data"].(map[string]any)
	if data["configured"] != false || data["source"] != nil {
		t.Errorf("unconfigured = %v", data)
	}

	token := fake.setToken(t)
	if err := h.store.SetSecret("simplefin_token", "", token); err != nil {
		t.Fatal(err)
	}
	data = jsonBody(t, h.postJSON(t, "/simplefin/status", nil, h.userToken))["data"].(map[string]any)
	if data["configured"] != true || data["source"] != "global" {
		t.Errorf("global = %v", data)
	}

	if err := h.store.SetSecret("simplefin_token", "", "Forbidden (was it already claimed?)"); err != nil {
		t.Fatal(err)
	}
	data = jsonBody(t, h.postJSON(t, "/simplefin/status", nil, h.userToken))["data"].(map[string]any)
	if data["configured"] != false {
		t.Errorf("forbidden token = %v", data)
	}

	// Per-budget credentials take precedence and are identified as such.
	if err := h.store.SetSecret("simplefin_token", "file-1", token); err != nil {
		t.Fatal(err)
	}
	data = jsonBody(t, h.postJSONWithFile(t, "/simplefin/status", nil, h.adminToken, "file-1"))["data"].(map[string]any)
	if data["configured"] != true || data["source"] != "per-budget-file" {
		t.Errorf("per budget = %v", data)
	}
}

func TestSimpleFINClaimAndAccounts(t *testing.T) {
	h, fake := newSimpleFINHarness(t, true)
	token := fake.setToken(t)
	if err := h.store.SetSecret("simplefin_token", "", token); err != nil {
		t.Fatal(err)
	}

	expectedKey := simplefinAccessKey(fake, "user", "password")
	fake.claimBody = expectedKey + "\n"
	response := h.postJSON(t, "/simplefin/accounts", nil, h.userToken)
	if response.Code != 200 {
		t.Fatalf("accounts status = %d, body = %q", response.Code, response.Body.String())
	}
	data := jsonBody(t, response)["data"].(map[string]any)
	list := data["accounts"].([]any)
	if len(list) != 2 {
		t.Fatalf("accounts = %v", list)
	}
	first := list[0].(map[string]any)
	org := first["org"].(map[string]any)
	if first["name"] != "Checking" || first["custom-field"] != "kept" || org["domain"] != "bank.example" || org["id"] != "org-1" {
		t.Errorf("raw account fields missing: %v", first)
	}

	stored, ok, err := h.store.Secret("simplefin_accessKey", "")
	if err != nil || !ok || stored != expectedKey {
		t.Errorf("stored access key = %q, %v, %v", stored, ok, err)
	}
	claim := fake.last("/claim")
	if claim.method != http.MethodPost || claim.auth != "" {
		t.Errorf("claim request = %+v", claim)
	}
	accounts := fake.last("/accounts")
	if accounts.query != "balances-only=1" {
		t.Errorf("accounts query = %q", accounts.query)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:password"))
	if accounts.auth != wantAuth {
		t.Errorf("accounts auth = %q, want %q", accounts.auth, wantAuth)
	}

	// A valid cached key is used without claiming again.
	fake.mu.Lock()
	fake.requests = nil
	fake.mu.Unlock()
	if response := h.postJSON(t, "/simplefin/accounts", nil, h.userToken); response.Code != 200 {
		t.Fatalf("cached accounts status = %d", response.Code)
	}
	if fake.count("/claim") != 0 {
		t.Error("claimed again with a valid cached key")
	}

	// A stale Forbidden key is re-claimed.
	if err := h.store.SetSecret("simplefin_accessKey", "", "Forbidden (was it already claimed?)"); err != nil {
		t.Fatal(err)
	}
	fake.claimBody = expectedKey
	if response := h.postJSON(t, "/simplefin/accounts", nil, h.userToken); response.Code != 200 {
		t.Fatalf("re-claim status = %d", response.Code)
	}
	stored, _, _ = h.store.Secret("simplefin_accessKey", "")
	if stored != expectedKey {
		t.Errorf("re-claimed key = %q", stored)
	}
}

func TestSimpleFINClaimErrors(t *testing.T) {
	cases := []struct {
		name        string
		claimBody   string
		claimStatus int
		wantReason  string
	}{
		{"forbidden body", "Forbidden (was it already claimed?)", 200, "INVALID_ACCESS_TOKEN"},
		{"blank body", "   \n", 200, "INVALID_ACCESS_TOKEN"},
		{"not a url", "<html>Service unavailable</html>", 200, "INVALID_ACCESS_TOKEN"},
		{"forbidden status", "Forbidden (was it already claimed?)", 403, "INVALID_ACCESS_TOKEN"},
		{"redirect", "", 302, "SERVER_DOWN"},
		{"server error", "<html>Bad Gateway</html>", 502, "SERVER_DOWN"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h, fake := newSimpleFINHarness(t, true)
			if err := h.store.SetSecret("simplefin_token", "", fake.server.URL); err != nil {
				t.Fatal(err)
			}
			fake.claimBody = testCase.claimBody
			fake.claimStatus = testCase.claimStatus

			token := base64.StdEncoding.EncodeToString([]byte(fake.server.URL + "/claim"))
			if err := h.store.SetSecret("simplefin_token", "", token); err != nil {
				t.Fatal(err)
			}
			response := h.postJSON(t, "/simplefin/accounts", nil, h.userToken)
			data := jsonBody(t, response)["data"].(map[string]any)
			if data["error_code"] != testCase.wantReason {
				t.Errorf("error_code = %v, body = %q", data["error_code"], response.Body.String())
			}
			if stored, ok, _ := h.store.Secret("simplefin_accessKey", ""); ok && stored != "" {
				t.Errorf("invalid claim persisted %q", stored)
			}
		})
	}

	t.Run("undecodable token", func(t *testing.T) {
		h, fake := newSimpleFINHarness(t, true)
		if err := h.store.SetSecret("simplefin_token", "", base64.StdEncoding.EncodeToString([]byte("not a claim url"))); err != nil {
			t.Fatal(err)
		}
		response := h.postJSON(t, "/simplefin/accounts", nil, h.userToken)
		if data := jsonBody(t, response)["data"].(map[string]any); data["error_code"] != "INVALID_ACCESS_TOKEN" {
			t.Errorf("data = %v", data)
		}
		if fake.count("/claim") != 0 {
			t.Error("contacted SimpleFIN with an invalid claim URL")
		}
	})

	t.Run("private claim without an exception", func(t *testing.T) {
		h, fake := newSimpleFINHarness(t, false)
		token := base64.StdEncoding.EncodeToString([]byte(fake.server.URL + "/claim"))
		if err := h.store.SetSecret("simplefin_token", "", token); err != nil {
			t.Fatal(err)
		}
		response := h.postJSON(t, "/simplefin/accounts", nil, h.userToken)
		if data := jsonBody(t, response)["data"].(map[string]any); data["error_code"] != "SERVER_DOWN" {
			t.Errorf("data = %v", data)
		}
		if fake.count("/claim") != 0 {
			t.Error("contacted a blocked claim URL")
		}
	})
}

func TestSimpleFINTransactions(t *testing.T) {
	h, fake := newSimpleFINHarness(t, true)
	key := simplefinAccessKey(fake, "user", "password")
	if err := h.store.SetSecret("simplefin_accessKey", "", key); err != nil {
		t.Fatal(err)
	}

	response := h.postJSON(t, "/simplefin/transactions", map[string]any{
		"accountId": "account-1",
		"startDate": "2026-01-02",
	}, h.userToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	data := jsonBody(t, response)["data"].(map[string]any)
	if data["startingBalance"].(float64) != 12345 {
		t.Errorf("startingBalance = %v", data["startingBalance"])
	}
	transactions := data["transactions"].(map[string]any)
	all := transactions["all"].([]any)
	booked := transactions["booked"].([]any)
	pending := transactions["pending"].([]any)
	if len(all) != 2 || len(booked) != 1 || len(pending) != 1 {
		t.Fatalf("all/booked/pending = %d/%d/%d", len(all), len(booked), len(pending))
	}
	first := all[0].(map[string]any)
	if first["transactionId"] != "tx-pending" || first["booked"] != false || first["payeeName"] != "Pending Payee" {
		t.Errorf("pending transaction = %v", first)
	}
	second := all[1].(map[string]any)
	if second["transactionId"] != "tx-2" || second["transactionAmount"].(map[string]any)["amount"] != "1000.00" {
		t.Errorf("booked transaction = %v", second)
	}
	if second["date"] != "2026-01-02" || second["postedDate"] != "2026-01-02" {
		t.Errorf("dates = %v", second)
	}

	query, err := url.ParseQuery(fake.last("/accounts").query)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	wantEnd := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC).Unix()
	if got := query.Get("end-date"); got != strconv.FormatInt(wantEnd, 10) {
		t.Errorf("end-date = %q, want %d", got, wantEnd)
	}

	// Batch form returns one entry per account.
	response = h.postJSON(t, "/simplefin/transactions", map[string]any{
		"accountId": []string{"account-1", "account-2"},
		"startDate": []string{"2026-01-01", "2026-01-01"},
	}, h.userToken)
	data = jsonBody(t, response)["data"].(map[string]any)
	if data["account-1"] == nil || data["account-2"] == nil || data["errors"] != nil {
		t.Errorf("batch data = %v", data)
	}

	// Missing accounts are reported per account.
	response = h.postJSON(t, "/simplefin/transactions", map[string]any{
		"accountId": "missing",
		"startDate": "2026-01-01",
	}, h.userToken)
	data = jsonBody(t, response)["data"].(map[string]any)
	if data["error_type"] != "ACCOUNT_MISSING" {
		t.Errorf("missing account = %v", data)
	}

	// Array shape mismatches are provider-level errors.
	response = h.postJSON(t, "/simplefin/transactions", map[string]any{
		"accountId": []string{"account-1"},
		"startDate": "2026-01-01",
	}, h.userToken)
	data = jsonBody(t, response)["data"].(map[string]any)
	if data["error_code"] != "INTERNAL_ERROR" {
		t.Errorf("shape mismatch = %v", data)
	}

	// A 403 from the bridge is an invalid token.
	fake.accountsStatus = http.StatusForbidden
	response = h.postJSON(t, "/simplefin/transactions", map[string]any{
		"accountId": "account-1",
		"startDate": "2026-01-01",
	}, h.userToken)
	if data := jsonBody(t, response)["data"].(map[string]any); data["error_code"] != "INVALID_ACCESS_TOKEN" {
		t.Errorf("forbidden = %v", data)
	}
}

func TestSimpleFINRedirects(t *testing.T) {
	h, fake := newSimpleFINHarness(t, true)
	key := simplefinAccessKey(fake, "user", "password")
	if err := h.store.SetSecret("simplefin_accessKey", "", key); err != nil {
		t.Fatal(err)
	}

	fake.redirect = "http://[ff02::1]/accounts"
	response := h.postJSON(t, "/simplefin/accounts", nil, h.userToken)
	if data := jsonBody(t, response)["data"].(map[string]any); data["error_code"] != "SERVER_DOWN" {
		t.Errorf("private redirect = %v", data)
	}

	// A cross-origin redirect drops the bridge credentials.
	_, otherFake := newSimpleFINHarness(t, true)
	fake.redirect = otherFake.server.URL + "/accounts"
	response = h.postJSON(t, "/simplefin/accounts", nil, h.userToken)
	if response.Code != 200 {
		t.Fatalf("cross-origin redirect status = %d, body = %q", response.Code, response.Body.String())
	}
	if got := otherFake.last("/accounts").auth; got != "" {
		t.Errorf("cross-origin redirect forwarded Authorization %q", got)
	}
}

func TestSimpleFINFileAccessAndAPIKeys(t *testing.T) {
	h, fake := newSimpleFINHarness(t, true)
	key := simplefinAccessKey(fake, "user", "password")
	if err := h.store.SetSecret("simplefin_accessKey", "", key); err != nil {
		t.Fatal(err)
	}

	// Invalid and inaccessible file ids are rejected before any network call.
	if response := h.postJSONWithFile(t, "/simplefin/status", nil, h.userToken, "../invalid"); response.Code != http.StatusBadRequest {
		t.Errorf("invalid file id = %d", response.Code)
	}
	if response := h.postJSONWithFile(t, "/simplefin/status", nil, h.userToken, "file-1"); response.Code != http.StatusForbidden {
		t.Errorf("unshared file = %d", response.Code)
	}
	if fake.count("/accounts") != 0 {
		t.Fatal("status contacted the bridge")
	}

	h.grantAccess(t, h.user.Id, "file-1")
	if response := h.postJSONWithFile(t, "/simplefin/status", nil, h.userToken, "file-1"); response.Code != http.StatusOK {
		t.Errorf("shared file = %d", response.Code)
	}

	// Deleted budgets are denied even with retained credentials.
	record := h.fileRecord(t, "file-1")
	record.Set("deleted", true)
	if err := h.app.Save(record); err != nil {
		t.Fatal(err)
	}
	if response := h.postJSONWithFile(t, "/simplefin/status", nil, h.adminToken, "file-1"); response.Code != http.StatusForbidden {
		t.Errorf("deleted file = %d", response.Code)
	}

	// API keys require one of their granted, active budgets.
	h2, fake2 := newSimpleFINHarness(t, true)
	h2.standardFile(t, "file-2", "group-2", "key-id-2", h2.admin.Id)
	key2 := simplefinAccessKey(fake2, "user", "password")
	if err := h2.store.SetSecret("simplefin_accessKey", "file-2", key2); err != nil {
		t.Fatal(err)
	}
	_, secret, err := h2.store.CreateAPIKey(h2.admin.Id, "bank sync", []string{"file-2"})
	if err != nil {
		t.Fatal(err)
	}
	if response := h2.postJSONWithFile(t, "/simplefin/transactions", map[string]any{"accountId": "account-1", "startDate": "2026-01-01"}, secret, "file-2"); response.Code != 200 {
		t.Errorf("api key transaction = %d, body = %q", response.Code, response.Body.String())
	}
	if response := h2.postJSON(t, "/simplefin/transactions", map[string]any{"accountId": "account-1", "startDate": "2026-01-01"}, secret); response.Code != http.StatusForbidden {
		t.Errorf("api key without file = %d", response.Code)
	}
	if response := h2.postJSONWithFile(t, "/simplefin/transactions", map[string]any{"accountId": "account-1", "startDate": "2026-01-01"}, secret, "file-1"); response.Code != http.StatusForbidden {
		t.Errorf("api key for another file = %d", response.Code)
	}

	// An API key never falls back to global credentials.
	if err := h2.store.ResetSecret("simplefin_accessKey", "file-2"); err != nil {
		t.Fatal(err)
	}
	if err := h2.store.SetSecret("simplefin_accessKey", "", key2); err != nil {
		t.Fatal(err)
	}
	fake2.mu.Lock()
	fake2.requests = nil
	fake2.mu.Unlock()
	response := h2.postJSONWithFile(t, "/simplefin/transactions", map[string]any{"accountId": "account-1", "startDate": "2026-01-01"}, secret, "file-2")
	if data := jsonBody(t, response)["data"].(map[string]any); data["error_code"] != "INVALID_ACCESS_TOKEN" {
		t.Errorf("api key global fallback = %v", data)
	}
	if fake2.count("/accounts") != 0 {
		t.Error("api key used global credentials")
	}

	// A session still falls back to the global credentials.
	response = h2.postJSON(t, "/simplefin/transactions", map[string]any{"accountId": "account-1", "startDate": "2026-01-01"}, h2.adminToken)
	if response.Code != 200 {
		t.Errorf("session global fallback = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestSimpleFINNeedsAttention(t *testing.T) {
	h, fake := newSimpleFINHarness(t, true)
	key := simplefinAccessKey(fake, "user", "password")
	if err := h.store.SetSecret("simplefin_accessKey", "", key); err != nil {
		t.Fatal(err)
	}
	fake.accountsBody = strings.Replace(simplefinFixture, `"errors": []`, `"errors": ["Connection to Bank may need attention"]`, 1)

	response := h.postJSON(t, "/simplefin/transactions", map[string]any{
		"accountId": "account-1",
		"startDate": "2026-01-01",
	}, h.userToken)
	data := jsonBody(t, response)["data"].(map[string]any)
	if data["error_type"] != "ACCOUNT_NEEDS_ATTENTION" {
		t.Errorf("needs attention = %v", data)
	}
}

func TestSimpleFINQueryParameters(t *testing.T) {
	h, fake := newSimpleFINHarness(t, true)
	key := simplefinAccessKey(fake, "user", "password")
	if err := h.store.SetSecret("simplefin_accessKey", "", key); err != nil {
		t.Fatal(err)
	}
	h.postJSON(t, "/simplefin/transactions", map[string]any{
		"accountId": []string{"account-1", "account-2"},
		"startDate": []string{"2026-01-02", "2026-01-05"},
	}, h.userToken)

	parsed, err := url.ParseQuery(fake.last("/accounts").query)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Get("start-date") != "1767312000" {
		t.Errorf("start-date = %q", parsed.Get("start-date"))
	}
	if parsed.Get("pending") != "1" {
		t.Errorf("pending = %q", parsed.Get("pending"))
	}
	if got := parsed["account"]; len(got) != 2 || got[0] != "account-1" || got[1] != "account-2" {
		t.Errorf("account params = %v", got)
	}
}
