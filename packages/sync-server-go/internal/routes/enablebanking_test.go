package routes

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/banks"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
)

const enableBankingSessionFixture = `{
  "session_id": "session-1",
  "accounts": [
    {
      "account_id": {"iban": "FI0455231152453547"},
      "account_servicer": {"bic_fi": "NDEAFIHH", "name": "Nordea"},
      "name": "Current Account",
      "currency": "EUR",
      "uid": "account-1"
    }
  ],
  "aspsp": {"name": "Nordea", "country": "FI"}
}`

const enableBankingTransactionsFixture = `{
  "transactions": [
    {
      "entry_reference": "ref-1",
      "transaction_amount": {"currency": "EUR", "amount": "100.50"},
      "debtor": {"name": "My Employer"},
      "creditor": {"name": "Salary Inc"},
      "credit_debit_indicator": "CRDT",
      "status": "BOOK",
      "booking_date": "2026-03-01",
      "remittance_information": ["Monthly salary"]
    },
    {
      "entry_reference": "ref-2",
      "transaction_amount": {"currency": "EUR", "amount": "-25.99"},
      "creditor": {"name": "Grocery Store"},
      "credit_debit_indicator": "DBIT",
      "status": "BOOK",
      "booking_date": "2026-03-02",
      "remittance_information": ["Groceries"]
    },
    {
      "transaction_id": "ref-3",
      "transaction_amount": {"currency": "EUR", "amount": "-10.00"},
      "status": "PDNG",
      "value_date": "2026-03-03",
      "remittance_information": ["Card payment"]
    }
  ]
}`

type fakeEnableBanking struct {
	server *httptest.Server

	mu                 sync.Mutex
	authorization      string
	applicationStatus  int
	aspspsStatus       int
	transactionsStatus int
	authBody           map[string]any
	sessionCalls       int
	transactionDate    string
	psuIP              string
}

func newFakeEnableBanking(t *testing.T) *fakeEnableBanking {
	t.Helper()
	fake := &fakeEnableBanking{applicationStatus: 200, aspspsStatus: 200, transactionsStatus: 200}

	mux := http.NewServeMux()
	mux.HandleFunc("/application", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.authorization = r.Header.Get("Authorization")
		status := fake.applicationStatus
		fake.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"id":"application"}`))
	})
	mux.HandleFunc("/aspsps", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		status := fake.aspspsStatus
		fake.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`[{"name":"Nordea","country":"FI"}]`))
	})
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		_ = jsonDecode(r, &body)
		fake.mu.Lock()
		fake.authBody = body
		fake.mu.Unlock()
		_, _ = w.Write([]byte(`{"url":"https://bank.example/auth","authorization_id":"auth-1"}`))
	})
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.sessionCalls++
		fake.mu.Unlock()
		_, _ = w.Write([]byte(enableBankingSessionFixture))
	})
	mux.HandleFunc("/accounts/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/balances") {
			_, _ = w.Write([]byte(`{"balances":[{"balance_amount":{"currency":"EUR","amount":"1234.56"},"balance_type":"CLAV","reference_date":"2026-03-24"}]}`))
			return
		}
		fake.mu.Lock()
		fake.transactionDate = r.URL.Query().Get("date_from")
		fake.psuIP = r.Header.Get("Psu-Ip-Address")
		status := fake.transactionsStatus
		fake.mu.Unlock()
		if status != 200 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"expired_session","message":"Session expired"}`))
			return
		}
		_, _ = w.Write([]byte(enableBankingTransactionsFixture))
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func jsonDecode(r *http.Request, dst any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst)
}

func newEnableBankingHarness(t *testing.T, configure ...func(*config.Config)) (*harness, *fakeEnableBanking) {
	t.Helper()
	fake := newFakeEnableBanking(t)
	restore := banks.SetEnableBankingBaseURLForTest(fake.server.URL)
	t.Cleanup(restore)

	h := newHarnessWithConfig(t, func(cfg *config.Config) {
		for _, apply := range configure {
			apply(cfg)
		}
	}, true)
	if err := h.store.SetSecret("enablebanking_applicationId", "", "app-id"); err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := h.store.SetSecret("enablebanking_secretKey", "", string(pemKey)); err != nil {
		t.Fatal(err)
	}
	return h, fake
}

func TestEnableBankingConfigureAndStatus(t *testing.T) {
	h, fake := newEnableBankingHarness(t)

	data := jsonBody(t, h.postJSON(t, "/enablebanking/status", nil, h.userToken))["data"].(map[string]any)
	if data["configured"] != true {
		t.Errorf("status = %v", data)
	}

	if response := h.postJSON(t, "/enablebanking/configure", map[string]any{"applicationId": "new", "secretKey": "key"}, h.userToken); response.Code != http.StatusForbidden {
		t.Errorf("basic configure = %d", response.Code)
	}

	if response := h.postJSON(t, "/enablebanking/configure", map[string]any{"applicationId": " new ", "secretKey": string(mustKey(t))}, h.adminToken); response.Code != 200 {
		t.Fatalf("configure = %d, body = %q", response.Code, response.Body.String())
	}
	if got := jsonBody(t, responseFromConfigure(h, t))["data"].(map[string]any)["configured"]; got != true {
		t.Errorf("configured = %v", got)
	}

	// The service signs RS256 with the application id as kid.
	token := strings.TrimPrefix(fake.authorization, "Bearer ")
	parsed, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse jwt: %v", err)
	}
	if parsed.Header["kid"] != "new" || parsed.Header["alg"] != "RS256" {
		t.Errorf("jwt header = %v", parsed.Header)
	}
}

func mustKey(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func responseFromConfigure(h *harness, t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return h.postJSON(t, "/enablebanking/status", nil, h.adminToken)
}

func TestEnableBankingAuthAndTransactions(t *testing.T) {
	h, fake := newEnableBankingHarness(t)

	response := h.postJSON(t, "/enablebanking/start-auth", map[string]any{
		"aspsp":       map[string]any{"name": "Nordea", "country": "FI"},
		"redirectUrl": "http://localhost:3000/cb",
	}, h.userToken)
	if response.Code != 200 {
		t.Fatalf("start auth = %d, body = %q", response.Code, response.Body.String())
	}
	data := jsonBody(t, response)["data"].(map[string]any)
	if data["url"] != "https://bank.example/auth" || data["state"] == "" {
		t.Errorf("start auth data = %v", data)
	}
	if fake.authBody["psu_type"] != "personal" {
		t.Errorf("auth body = %v", fake.authBody)
	}

	response = h.postJSON(t, "/enablebanking/transactions", map[string]any{
		"accountId": "account-1",
		"startDate": "2026-03-01",
	}, h.userToken)
	if response.Code != 200 {
		t.Fatalf("transactions = %d, body = %q", response.Code, response.Body.String())
	}
	data = jsonBody(t, response)["data"].(map[string]any)
	if data["startingBalance"].(float64) != 123456 {
		t.Errorf("startingBalance = %v", data["startingBalance"])
	}
	transactions := data["transactions"].(map[string]any)
	if len(transactions["all"].([]any)) != 3 || len(transactions["booked"].([]any)) != 2 || len(transactions["pending"].([]any)) != 1 {
		t.Errorf("transactions = %v", transactions)
	}

	state := "callback-state"
	request := httptest.NewRequest(http.MethodGet, "/enablebanking/auth_callback?code=abc&state="+state, nil)
	if got := h.do(request); got.Code != 200 || !strings.Contains(got.Body.String(), "Authorization successful") {
		t.Errorf("callback = %d %q", got.Code, got.Body.String())
	}
	poll := jsonBody(t, h.postJSON(t, "/enablebanking/poll-auth", map[string]any{"state": state}, h.userToken))["data"].(map[string]any)
	if poll["session_id"] != "session-1" {
		t.Errorf("poll = %v", poll)
	}

	// A 401 from the bank asks the client to re-authenticate.
	fake.transactionsStatus = http.StatusUnauthorized
	response = h.postJSON(t, "/enablebanking/transactions", map[string]any{"accountId": "account-1", "startDate": "2026-03-01"}, h.userToken)
	data = jsonBody(t, response)["data"].(map[string]any)
	if data["error_type"] != "ITEM_ERROR" || data["error_code"] != "ITEM_LOGIN_REQUIRED" {
		t.Errorf("expired session = %v", data)
	}
}

func TestEnableBankingConfigurationFailure(t *testing.T) {
	h, fake := newEnableBankingHarness(t)
	fake.applicationStatus = http.StatusUnauthorized

	response := h.postJSON(t, "/enablebanking/configure", map[string]any{"applicationId": "bad", "secretKey": string(mustKey(t))}, h.adminToken)
	data := jsonBody(t, response)["data"].(map[string]any)
	if data["error_code"] != "CONFIGURATION_FAILED" {
		t.Errorf("configure failure = %v", data)
	}

	if response := h.postJSON(t, "/enablebanking/configure", map[string]any{}, h.adminToken); response.Code != 200 {
		t.Errorf("missing input = %d", response.Code)
	}
}

func TestEnableBankingNumericDateAndTrustedPSUAddress(t *testing.T) {
	h, fake := newEnableBankingHarness(t, func(cfg *config.Config) {
		cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	})
	date := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(map[string]any{"accountId": "account-1", "startDate": date.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/enablebanking/transactions", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Actual-Token", h.userToken)
	request.Header.Set("X-Forwarded-For", "::ffff:8.8.8.8")
	request.RemoteAddr = "192.0.2.10:12345"
	if response := h.do(request); response.Code != http.StatusOK {
		t.Fatalf("request = %d %s", response.Code, response.Body)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.transactionDate != "2026-03-01" || fake.psuIP != "8.8.8.8" {
		t.Errorf("forwarded date = %q, PSU IP = %q", fake.transactionDate, fake.psuIP)
	}
}

type pollTestContext struct {
	context.Context
	reachedWait chan struct{}
	once        sync.Once
}

func (c *pollTestContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.reachedWait) })
	return c.Context.Done()
}

func TestEnableBankingSupersededPollKeepsReplacement(t *testing.T) {
	h, _ := newEnableBankingHarness(t)
	const state = "shared-state"

	startPoll := func() (<-chan *httptest.ResponseRecorder, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		t.Cleanup(cancel)
		watch := &pollTestContext{Context: ctx, reachedWait: make(chan struct{})}
		request := httptest.NewRequest(http.MethodPost, "/enablebanking/poll-auth", strings.NewReader(`{"state":"shared-state"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Actual-Token", h.userToken)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- h.do(request.WithContext(watch)) }()
		select {
		case <-watch.reachedWait:
		case <-ctx.Done():
			t.Error("poll did not enter its wait")
		}
		return done, cancel
	}

	first, cancelFirst := startPoll()
	second, _ := startPoll()
	select {
	case response := <-first:
		if data := jsonBody(t, response)["data"].(map[string]any); data["error"] != "Poll superseded" {
			t.Errorf("first poll = %v", data)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("first poll was not notified that it was superseded")
		cancelFirst()
		<-first
	}

	response := h.postJSON(t, "/enablebanking/complete-auth", map[string]string{"code": "abc", "state": state}, h.userToken)
	if response.Code != http.StatusOK {
		t.Errorf("complete-auth = %d %s", response.Code, response.Body)
	}
	response = <-second
	if result := jsonBody(t, response)["data"].(map[string]any); result["session_id"] != "session-1" {
		t.Errorf("replacement poll = %v", result)
	}
}
