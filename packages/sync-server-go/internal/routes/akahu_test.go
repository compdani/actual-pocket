package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/banks"
)

type fakeAkahu struct {
	server *httptest.Server

	mu           sync.Mutex
	startQueries []string
	refreshCalls int
	stale        bool
}

func newFakeAkahu(t *testing.T) *fakeAkahu {
	t.Helper()
	fake := &fakeAkahu{}

	account := func() string {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		refreshed := "2026-11-14T19:00:00.000Z"
		if fake.stale {
			refreshed = "2020-01-01T00:00:00.000Z"
		}
		return `{"success":true,"item":{"_id":"acc_1","balance":{"current":100,"available":80,"currency":"NZD"},"refreshed":{"balance":"2026-11-14T19:00:00.000Z","transactions":"` + refreshed + `"}}}`
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"items":[` + accountItem() + `]}`))
	})
	mux.HandleFunc("/accounts/acc_1/transactions/pending", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"items":[{"_id":"pending_1","date":"2026-11-14T20:00:00.000Z","amount":-3.5,"description":"Pending coffee"}]}`))
	})
	mux.HandleFunc("/accounts/acc_1/transactions", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.startQueries = append(fake.startQueries, r.URL.Query().Get("start"))
		fake.mu.Unlock()
		_, _ = w.Write([]byte(`{"success":true,"cursor":{},"items":[{"_id":"tx_1","date":"2026-11-14T14:00:00.000Z","amount":-12.5,"description":"Early NZ Coffee","merchant":{"name":"Coffee Shop"}}]}`))
	})
	mux.HandleFunc("/accounts/acc_1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(account()))
	})
	mux.HandleFunc("/refresh", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.refreshCalls++
		fake.stale = false
		fake.mu.Unlock()
		_, _ = w.Write([]byte(`{"success":true,"item":{}}`))
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func accountItem() string {
	return `{"_id":"acc_1","balance":{"current":100,"available":80,"currency":"NZD"},"refreshed":{"balance":"2026-11-14T19:00:00.000Z","transactions":"2026-11-14T19:00:00.000Z"}}`
}

func newAkahuHarness(t *testing.T) (*harness, *fakeAkahu) {
	t.Helper()
	fake := newFakeAkahu(t)
	restore := banks.SetAkahuBaseURLForTest(fake.server.URL)
	t.Cleanup(restore)
	banks.SetAkahuRefreshWaitForTest(time.Millisecond)

	h := newHarness(t)
	for name, value := range map[string]string{
		"akahu_userToken": "user-token",
		"akahu_appToken":  "app-token",
	} {
		if err := h.store.SetSecret(name, "", value); err != nil {
			t.Fatal(err)
		}
	}
	return h, fake
}

func TestAkahuStatusAndTransactions(t *testing.T) {
	h, fake := newAkahuHarness(t)

	data := jsonBody(t, h.postJSON(t, "/akahu/status", nil, h.userToken))["data"].(map[string]any)
	if data["configured"] != true {
		t.Errorf("status = %v", data)
	}

	response := h.postJSON(t, "/akahu/transactions", map[string]any{
		"accountId": "acc_1",
		"startDate": "2026-11-15",
	}, h.userToken)
	if response.Code != 200 {
		t.Fatalf("transactions = %d, body = %q", response.Code, response.Body.String())
	}
	data = jsonBody(t, response)["data"].(map[string]any)
	if data["startingBalance"].(float64) != 10000 {
		t.Errorf("startingBalance = %v", data["startingBalance"])
	}
	transactions := data["transactions"].(map[string]any)
	all := transactions["all"].([]any)
	if len(all) != 2 {
		t.Fatalf("transactions = %v", transactions)
	}
	var booked map[string]any
	for _, entry := range all {
		candidate := entry.(map[string]any)
		if candidate["transactionId"] == "tx_1" {
			booked = candidate
		}
	}
	if booked == nil || booked["payeeName"] != "Coffee Shop" || booked["date"] != "2026-11-15" {
		t.Errorf("booked = %v", booked)
	}
	if booked["transactionAmount"].(map[string]any)["amount"].(float64) != -12.5 {
		t.Errorf("amount = %v", booked["transactionAmount"])
	}

	fake.mu.Lock()
	start := fake.startQueries[0]
	fake.mu.Unlock()
	if start != "2026-11-14T10:59:59.999Z" {
		t.Errorf("start query = %q", start)
	}
}

func TestAkahuClampsFutureStartDate(t *testing.T) {
	h, fake := newAkahuHarness(t)

	future := time.Now().Add(48 * time.Hour).Format("2006-01-02")
	response := h.postJSON(t, "/akahu/transactions", map[string]any{
		"accountId": "acc_1",
		"startDate": future,
	}, h.userToken)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}

	fake.mu.Lock()
	start := fake.startQueries[0]
	fake.mu.Unlock()
	parsed, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		t.Fatalf("parse start %q: %v", start, err)
	}
	if parsed.After(time.Now()) {
		t.Errorf("start %s is in the future", parsed)
	}
}

func TestAkahuRefreshAndMissingBalance(t *testing.T) {
	h, fake := newAkahuHarness(t)
	fake.mu.Lock()
	fake.stale = true
	fake.mu.Unlock()

	response := h.postJSON(t, "/akahu/transactions", map[string]any{
		"accountId": "acc_1",
		"startDate": "2026-11-15",
	}, h.userToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	fake.mu.Lock()
	refreshes := fake.refreshCalls
	fake.mu.Unlock()
	if refreshes != 1 {
		t.Errorf("refresh calls = %d, want 1", refreshes)
	}

	if err := h.store.ResetSecret("akahu_userToken", ""); err != nil {
		t.Fatal(err)
	}
	response = h.postJSON(t, "/akahu/accounts", nil, h.userToken)
	data := jsonBody(t, response)["data"].(map[string]any)
	if data["error"] != "Missing user or app token" {
		t.Errorf("missing token = %v", data)
	}

	// API keys are not accepted; only sessions may call Akahu.
	_, secret, err := h.store.CreateAPIKey(h.admin.Id, "akahu", nil)
	if err != nil {
		t.Fatal(err)
	}
	if response := h.postJSON(t, "/akahu/status", nil, secret); response.Code != http.StatusUnauthorized {
		t.Errorf("api key status = %d", response.Code)
	}
}

func TestAkahuErrors(t *testing.T) {
	h, _ := newAkahuHarness(t)
	response := h.postJSON(t, "/akahu/transactions", map[string]any{"accountId": "acc_1"}, h.userToken)
	if !strings.Contains(response.Body.String(), "accountId and startDate are required") {
		t.Errorf("missing params = %q", response.Body.String())
	}
}
