package routes

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/banks"
)

const pluggyAccountFixture = `{
  "id": "account-1",
  "itemId": "item-1",
  "name": "Checking",
  "type": "CHECKING",
  "balance": 100.25,
  "currencyCode": "USD",
  "updatedAt": "2026-03-03T00:00:00.000Z"
}`

const pluggyTransactionsFixture = `{
  "results": [
    {
      "id": "tx-1",
      "date": "2026-03-01T00:00:00.000Z",
      "description": "Coffee",
      "descriptionRaw": "Coffee Shop",
      "amount": -10.5,
      "amountInAccountCurrency": null,
      "currencyCode": "USD",
      "status": "POSTED",
      "type": "DEBIT",
      "merchant": {"name": "Coffee Shop"}
    }
  ],
  "next": "/v2/transactions?accountId=account-1&after=next%2Fpage"
}`

const pluggySecondPageFixture = `{
  "results": [
    {
      "id": "tx-2",
      "date": "2026-03-02T00:00:00.000Z",
      "description": "Pending purchase",
      "amount": -5,
      "currencyCode": "USD",
      "status": "PENDING"
    }
  ],
  "next": null
}`

type fakePluggy struct {
	server *httptest.Server

	mu             sync.Mutex
	apiKeys        []string
	authCalls      int
	rejectFirstKey bool
}

func newFakePluggy(t *testing.T) *fakePluggy {
	t.Helper()
	fake := &fakePluggy{}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.authCalls++
		call := fake.authCalls
		fake.mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"apiKey":"pluggy-key-%d"}`, call)
	})
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		key := r.Header.Get("X-API-KEY")
		fake.apiKeys = append(fake.apiKeys, key)
		reject := fake.rejectFirstKey && key == "pluggy-key-1"
		fake.mu.Unlock()
		if reject {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"results":[` + pluggyAccountFixture + `],"total":1}`))
	})
	mux.HandleFunc("/v2/transactions", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("accountId") != "account-1" || query.Get("dateFrom") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch query.Get("after") {
		case "":
			_, _ = w.Write([]byte(pluggyTransactionsFixture))
		case "next/page":
			_, _ = w.Write([]byte(pluggySecondPageFixture))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	mux.HandleFunc("/accounts/account-1", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.apiKeys = append(fake.apiKeys, r.Header.Get("X-API-KEY"))
		fake.mu.Unlock()
		_, _ = w.Write([]byte(pluggyAccountFixture))
	})
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func newPluggyHarness(t *testing.T) (*harness, *fakePluggy) {
	t.Helper()
	fake := newFakePluggy(t)
	restore := banks.SetPluggyBaseURLForTest(fake.server.URL)
	t.Cleanup(restore)

	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)
	for name, value := range map[string]string{
		"pluggyai_clientId":     "client-id",
		"pluggyai_clientSecret": "client-secret",
		"pluggyai_itemIds":      "item-1",
	} {
		if err := h.store.SetSecret(name, "", value); err != nil {
			t.Fatal(err)
		}
	}
	return h, fake
}

func TestPluggyStatusAndTransactions(t *testing.T) {
	h, fake := newPluggyHarness(t)

	data := jsonBody(t, h.postJSON(t, "/pluggyai/status", nil, h.userToken))["data"].(map[string]any)
	if data["configured"] != true || data["source"] != "global" {
		t.Errorf("status = %v", data)
	}

	response := h.postJSON(t, "/pluggyai/transactions", map[string]any{
		"accountId": "account-1",
		"startDate": "2026-03-01",
	}, h.userToken)
	if response.Code != 200 {
		t.Fatalf("transactions = %d, body = %q", response.Code, response.Body.String())
	}
	data = jsonBody(t, response)["data"].(map[string]any)
	if data["startingBalance"] != float64(10025) {
		t.Fatalf("transactions response = %v", data)
	}
	transactions := data["transactions"].(map[string]any)
	all := transactions["all"].([]any)
	if len(all) != 2 || len(transactions["booked"].([]any)) != 1 || len(transactions["pending"].([]any)) != 1 {
		t.Fatalf("transactions = %v", transactions)
	}
	first := all[0].(map[string]any)
	if first["transactionId"] != "tx-2" || first["booked"] != false {
		t.Errorf("first = %v", first)
	}
	second := all[1].(map[string]any)
	if second["payeeName"] != "Coffee Shop" || second["transactionAmount"].(map[string]any)["amount"].(float64) != -10.5 {
		t.Errorf("second = %v", second)
	}
	if fake.authCalls != 1 {
		t.Errorf("auth calls = %d, want 1 (cached)", fake.authCalls)
	}
	if len(fake.apiKeys) == 0 || fake.apiKeys[0] != "pluggy-key-1" {
		t.Errorf("api key header = %v", fake.apiKeys)
	}
}

func TestPluggyPerBudgetAndAPIKey(t *testing.T) {
	h, _ := newPluggyHarness(t)
	for name, value := range map[string]string{
		"pluggyai_clientId":     "budget-client",
		"pluggyai_clientSecret": "budget-secret",
		"pluggyai_itemIds":      "item-1",
	} {
		if err := h.store.SetSecret(name, "file-1", value); err != nil {
			t.Fatal(err)
		}
	}

	data := jsonBody(t, h.postJSONWithFile(t, "/pluggyai/status", nil, h.adminToken, "file-1"))["data"].(map[string]any)
	if data["source"] != "per-budget-file" {
		t.Errorf("per budget status = %v", data)
	}

	// API keys may only use an accessible budget and never global credentials.
	_, secret, err := h.store.CreateAPIKey(h.admin.Id, "pluggy key", []string{"file-1"})
	if err != nil {
		t.Fatal(err)
	}
	if response := h.postJSON(t, "/pluggyai/transactions", map[string]any{"accountId": "account-1", "startDate": "2026-03-01"}, secret); response.Code != http.StatusForbidden {
		t.Errorf("api key without file = %d", response.Code)
	}

	for name := range map[string]bool{"pluggyai_clientId": true, "pluggyai_clientSecret": true, "pluggyai_itemIds": true} {
		if err := h.store.ResetSecret(name, "file-1"); err != nil {
			t.Fatal(err)
		}
	}
	response := h.postJSONWithFile(t, "/pluggyai/transactions", map[string]any{"accountId": "account-1", "startDate": "2026-03-01"}, secret, "file-1")
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "not-configured" {
		t.Errorf("api key without budget creds = %d %q", response.Code, response.Body.String())
	}

	// A deleted budget is denied before credential selection.
	record := h.fileRecord(t, "file-1")
	record.Set("deleted", true)
	if err := h.app.Save(record); err != nil {
		t.Fatal(err)
	}
	if response := h.postJSONWithFile(t, "/pluggyai/status", nil, h.adminToken, "file-1"); response.Code != http.StatusForbidden {
		t.Errorf("deleted budget = %d", response.Code)
	}
}

func TestPluggyRejectsAccountOutsideItems(t *testing.T) {
	h, fake := newPluggyHarness(t)
	_, secret, err := h.store.CreateAPIKey(h.admin.Id, "pluggy key", []string{"file-1"})
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"pluggyai_clientId":     "budget-client",
		"pluggyai_clientSecret": "budget-secret",
		"pluggyai_itemIds":      "other-item",
	} {
		if err := h.store.SetSecret(name, "file-1", value); err != nil {
			t.Fatal(err)
		}
	}
	response := h.postJSONWithFile(t, "/pluggyai/transactions", map[string]any{"accountId": "account-1", "startDate": "2026-03-01"}, secret, "file-1")
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "file-access-denied") {
		t.Errorf("outside items = %d %q", response.Code, response.Body.String())
	}
	if fake.authCalls == 0 {
		t.Error("expected the fake Pluggy service to be used")
	}
}

func TestPluggyRenewsRejectedAPIKey(t *testing.T) {
	h, fake := newPluggyHarness(t)
	response := h.postJSON(t, "/pluggyai/accounts", nil, h.userToken)
	if data := jsonBody(t, response)["data"].(map[string]any); data["accounts"] == nil {
		t.Fatalf("initial fetch = %v", data)
	}
	fake.mu.Lock()
	fake.rejectFirstKey = true
	fake.mu.Unlock()
	response = h.postJSON(t, "/pluggyai/accounts", nil, h.userToken)
	if data := jsonBody(t, response)["data"].(map[string]any); data["accounts"] == nil {
		t.Errorf("fetch after token rejection = %v", data)
	}
	if fake.authCalls != 2 {
		t.Errorf("auth calls = %d, want 2", fake.authCalls)
	}
}
