package banks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/ssrf"
)

const (
	enableBankingApplicationSecret = "enablebanking_applicationId"
	enableBankingSecretKeySecret   = "enablebanking_secretKey"
)

var enableBankingBaseURL = "https://api.enablebanking.com"

// SetEnableBankingBaseURLForTest points the service at a fake API. Tests use it.
func SetEnableBankingBaseURLForTest(value string) func() {
	previous := enableBankingBaseURL
	enableBankingBaseURL = strings.TrimRight(value, "/")
	return func() { enableBankingBaseURL = previous }
}

type enableBankingProvider struct {
	opts   Options
	client *http.Client

	mu        sync.Mutex
	completed map[string]*enableBankingCompletion
	pending   map[string]chan any
}

type enableBankingCompletion struct {
	result any
}

type enableBankingError struct {
	ErrorType string
	ErrorCode string
	Message   string
}

func (e *enableBankingError) Error() string { return e.Message }

func registerEnableBanking(actual *router.RouterGroup[*core.RequestEvent], opts Options) {
	provider := &enableBankingProvider{
		opts:      opts,
		client:    &http.Client{Timeout: 60 * time.Second},
		completed: map[string]*enableBankingCompletion{},
		pending:   map[string]chan any{},
	}

	actual.GET("/enablebanking/auth_callback", provider.authCallback)
	actual.POST("/enablebanking/status", provider.status)
	actual.POST("/enablebanking/configure", provider.configure)
	actual.POST("/enablebanking/aspsps", provider.aspsps)
	actual.POST("/enablebanking/start-auth", provider.startAuth)
	actual.POST("/enablebanking/complete-auth", provider.completeAuth)
	actual.POST("/enablebanking/poll-auth", provider.pollAuth)
	actual.POST("/enablebanking/transactions", provider.transactions)
}

func (p *enableBankingProvider) credentials() (string, string, error) {
	applicationID, applicationExists, err := p.opts.Store.Secret(enableBankingApplicationSecret, "")
	if err != nil {
		return "", "", err
	}
	secretKey, secretExists, err := p.opts.Store.Secret(enableBankingSecretKeySecret, "")
	if err != nil {
		return "", "", err
	}
	if !applicationExists || !secretExists || applicationID == "" || secretKey == "" {
		return "", "", &enableBankingError{ErrorType: "INVALID_INPUT", ErrorCode: "NOT_CONFIGURED", Message: "Enable Banking is not configured"}
	}
	return applicationID, secretKey, nil
}

func enableBankingJWT(applicationID, secretKey string) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(secretKey))
	if err != nil {
		return "", err
	}

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "enablebanking.com",
		"aud": "api.enablebanking.com",
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	})
	token.Header["typ"] = "JWT"
	token.Header["kid"] = applicationID
	return token.SignedString(key)
}

func (p *enableBankingProvider) authorizationHeader() (string, error) {
	applicationID, secretKey, err := p.credentials()
	if err != nil {
		return "", err
	}
	token, err := enableBankingJWT(applicationID, secretKey)
	if err != nil {
		return "", err
	}
	return "Bearer " + token, nil
}

func (p *enableBankingProvider) request(method, path string, body any, authOverride string, psuHeaders map[string]string) ([]byte, error) {
	headers := map[string]string{"Content-Type": "application/json"}
	if authOverride != "" {
		headers["Authorization"] = authOverride
	} else {
		authorization, err := p.authorizationHeader()
		if err != nil {
			return nil, err
		}
		headers["Authorization"] = authorization
	}
	for key, value := range psuHeaders {
		if value != "" {
			headers[key] = value
		}
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequest(method, enableBankingBaseURL+path, payload)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := p.client.Do(request)
	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			return nil, &enableBankingError{ErrorType: "TIMED_OUT", ErrorCode: "TIMED_OUT", Message: "Request timed out"}
		}
		return nil, err
	}
	defer response.Body.Close()

	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var parsed map[string]any
		_ = json.Unmarshal(raw, &parsed)
		message := string(raw)
		if value, ok := parsed["message"].(string); ok {
			message = value
		}
		errorType, _ := parsed["error"].(string)
		if errorType == "" {
			errorType = "UNKNOWN"
		}

		code := "INTERNAL_ERROR"
		switch {
		case response.StatusCode == 401 || response.StatusCode == 403:
			code = "INVALID_ACCESS_TOKEN"
		case response.StatusCode == 429:
			code = "RATE_LIMIT_EXCEEDED"
		case response.StatusCode == 404:
			code = "NOT_FOUND"
		case response.StatusCode >= 400 && response.StatusCode < 500:
			lower := strings.ToLower(errorType + " " + message)
			if strings.Contains(lower, "session") || strings.Contains(lower, "expired") {
				code = "INVALID_ACCESS_TOKEN"
			} else {
				code = "INVALID_INPUT"
			}
		}
		return nil, &enableBankingError{ErrorType: errorType, ErrorCode: code, Message: message}
	}

	return raw, nil
}

func (p *enableBankingProvider) status(e *core.RequestEvent) error {
	if p.opts.Auth.ResolveSession(e, "") == nil {
		return nil
	}
	_, _, err := p.credentials()
	configured := err == nil
	return respond(e, struct {
		Configured bool `json:"configured"`
	}{Configured: configured})
}

func (p *enableBankingProvider) configure(e *core.RequestEvent) error {
	session := p.opts.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}
	if session.Record.GetString("role") != "ADMIN" && !session.Record.GetBool("owner") {
		return httpx.FailDetails(e, http.StatusForbidden, "forbidden", "permission-not-found")
	}

	body := struct {
		ApplicationID string `json:"applicationId"`
		SecretKey     string `json:"secretKey"`
	}{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	applicationID := strings.TrimSpace(body.ApplicationID)
	if applicationID == "" || body.SecretKey == "" {
		return respond(e, struct {
			ErrorCode string `json:"error_code"`
			ErrorType string `json:"error_type"`
		}{"INVALID_INPUT", "Missing applicationId or secretKey"})
	}

	token, err := enableBankingJWT(applicationID, body.SecretKey)
	if err != nil {
		return respond(e, struct {
			ErrorCode string `json:"error_code"`
			ErrorType string `json:"error_type"`
		}{"CONFIGURATION_FAILED", err.Error()})
	}
	if _, err := p.request(http.MethodGet, "/application", nil, "Bearer "+token, nil); err != nil {
		return respond(e, struct {
			ErrorCode string `json:"error_code"`
			ErrorType string `json:"error_type"`
		}{"CONFIGURATION_FAILED", err.Error()})
	}

	if err := p.opts.Store.SetSecret(enableBankingApplicationSecret, "", applicationID); err != nil {
		return bankInternalError(e, err)
	}
	if err := p.opts.Store.SetSecret(enableBankingSecretKeySecret, "", body.SecretKey); err != nil {
		return bankInternalError(e, err)
	}

	return respond(e, struct {
		Configured bool `json:"configured"`
	}{Configured: true})
}

func (p *enableBankingProvider) aspsps(e *core.RequestEvent) error {
	if p.opts.Auth.ResolveSession(e, "") == nil {
		return nil
	}
	body := struct {
		Country string `json:"country"`
	}{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	path := "/aspsps"
	if body.Country != "" {
		path += "?country=" + url.QueryEscape(body.Country)
	}
	raw, err := p.request(http.MethodGet, path, nil, "", nil)
	if err != nil {
		return respond(e, struct {
			Error string `json:"error"`
		}{Error: err.Error()})
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		return bankInternalError(e, err)
	}
	return respond(e, result)
}

func (p *enableBankingProvider) startAuth(e *core.RequestEvent) error {
	session := p.opts.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}
	body := struct {
		ASPSP              struct{ Name, Country string } `json:"aspsp"`
		RedirectURL        string                         `json:"redirectUrl"`
		MaxConsentValidity int64                          `json:"maxConsentValidity"`
		PSUType            string                         `json:"psuType"`
	}{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}
	if body.ASPSP.Name == "" || body.RedirectURL == "" {
		return respond(e, struct {
			ErrorCode string `json:"error_code"`
			ErrorType string `json:"error_type"`
		}{"INVALID_INPUT", "Missing aspsp or redirectUrl"})
	}

	state := uuid.NewString()
	consentSeconds := int64(90 * 24 * 60 * 60)
	if body.MaxConsentValidity > 0 && body.MaxConsentValidity < consentSeconds {
		consentSeconds = body.MaxConsentValidity
	}
	psuType := "personal"
	if body.PSUType == "business" {
		psuType = "business"
	}

	payload := map[string]any{
		"aspsp":        map[string]any{"name": body.ASPSP.Name, "country": body.ASPSP.Country},
		"redirect_url": body.RedirectURL,
		"state":        state,
		"access": map[string]any{
			"valid_until":  time.Now().Add(time.Duration(consentSeconds) * time.Second).UTC().Format(time.RFC3339),
			"balances":     true,
			"transactions": true,
		},
		"psu_type": psuType,
	}

	raw, err := p.request(http.MethodPost, "/auth", payload, "", p.psuHeaders(e))
	if err != nil {
		return respond(e, struct {
			Error string `json:"error"`
		}{Error: err.Error()})
	}
	var auth struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &auth); err != nil {
		return bankInternalError(e, err)
	}

	return respond(e, struct {
		URL   string `json:"url"`
		State string `json:"state"`
	}{URL: auth.URL, State: state})
}

func (p *enableBankingProvider) authCallback(e *core.RequestEvent) error {
	query := e.Request.URL.Query()
	code := query.Get("code")
	state := query.Get("state")
	if code == "" {
		return httpx.Text(e, http.StatusBadRequest, "<html><body><p>Authorization failed: missing code.</p></body></html>")
	}
	if state == "" {
		return httpx.Text(e, http.StatusBadRequest, "<html><body><p>Authorization failed: missing state parameter.</p></body></html>")
	}

	result, err := p.sessionResult(code, p.psuHeaders(e))
	if err != nil {
		p.finishAuth(state, map[string]any{"error": err.Error()})
		return httpx.Text(e, http.StatusInternalServerError,
			"<html><body><p>Authorization failed. You can close this window and try again.</p></body></html>")
	}

	p.finishAuth(state, result)
	return httpx.Text(e, http.StatusOK,
		"<html><body><p>Authorization successful. This window will close.</p><script>setTimeout(function(){window.close()},1000)</script></body></html>")
}

func (p *enableBankingProvider) completeAuth(e *core.RequestEvent) error {
	if p.opts.Auth.ResolveSession(e, "") == nil {
		return nil
	}
	body := struct {
		Code  string `json:"code"`
		State string `json:"state"`
	}{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}
	if body.Code == "" {
		return respond(e, struct {
			ErrorCode string `json:"error_code"`
			ErrorType string `json:"error_type"`
		}{"INVALID_INPUT", "Missing code"})
	}

	result, err := p.sessionResult(body.Code, p.psuHeaders(e))
	if err != nil {
		errorResult := map[string]any{"error": err.Error()}
		if body.State != "" {
			p.finishAuth(body.State, errorResult)
		}
		return respond(e, errorResult)
	}
	if body.State != "" {
		p.finishAuth(body.State, result)
	}
	return respond(e, result)
}

func (p *enableBankingProvider) pollAuth(e *core.RequestEvent) error {
	if p.opts.Auth.ResolveSession(e, "") == nil {
		return nil
	}
	body := struct {
		State string `json:"state"`
	}{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}
	if body.State == "" {
		return respond(e, struct {
			ErrorCode string `json:"error_code"`
			ErrorType string `json:"error_type"`
		}{"INVALID_INPUT", "Missing state"})
	}

	p.mu.Lock()
	if completed, ok := p.completed[body.State]; ok {
		delete(p.completed, body.State)
		p.mu.Unlock()
		return respond(e, completed.result)
	}

	if previous := p.pending[body.State]; previous != nil {
		previous <- map[string]any{"error": "Poll superseded"}
	}
	wait := make(chan any, 1)
	p.pending[body.State] = wait
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		if p.pending[body.State] == wait {
			delete(p.pending, body.State)
		}
		p.mu.Unlock()
	}()

	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	select {
	case result := <-wait:
		return respond(e, result)
	case <-timer.C:
		return respond(e, map[string]any{"error": "Polling timed out"})
	case <-e.Request.Context().Done():
		return nil
	}
}

func (p *enableBankingProvider) finishAuth(state string, result any) {
	p.mu.Lock()
	defer p.mu.Unlock()

	completed := &enableBankingCompletion{result: result}
	p.completed[state] = completed
	time.AfterFunc(30*time.Second, func() {
		p.mu.Lock()
		if p.completed[state] == completed {
			delete(p.completed, state)
		}
		p.mu.Unlock()
	})

	if wait := p.pending[state]; wait != nil {
		wait <- result
		delete(p.pending, state)
	}
}

func (p *enableBankingProvider) sessionResult(code string, headers map[string]string) (map[string]any, error) {
	raw, err := p.request(http.MethodPost, "/sessions", map[string]any{"code": code}, "", nil)
	if err != nil {
		return nil, err
	}
	var session enableBankingSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return nil, err
	}

	accounts := make([]map[string]any, 0, len(session.Accounts))
	for _, account := range session.Accounts {
		normalized := normalizeEnableBankingAccount(account, session.ASPSP)
		normalizedBalances := []enableBankingNormalizedBalance{}
		balanceRaw, err := p.request(http.MethodGet, "/accounts/"+url.PathEscape(account.UID)+"/balances", nil, "", dataAccessPSUHeaders(session.ASPSP.Name, headers))
		if err == nil {
			var response struct {
				Balances []enableBankingBalance `json:"balances"`
			}
			if json.Unmarshal(balanceRaw, &response) == nil {
				for _, balance := range response.Balances {
					normalizedBalances = append(normalizedBalances, normalizeEnableBankingBalance(balance))
				}
			}
		}
		var preferred *enableBankingNormalizedBalance
		for index := range normalizedBalances {
			if normalizedBalances[index].BalanceType == "CLAV" {
				preferred = &normalizedBalances[index]
				break
			}
		}
		if preferred == nil && len(normalizedBalances) > 0 {
			preferred = &normalizedBalances[0]
		}
		amount := any(0)
		if preferred != nil {
			amount = preferred.BalanceAmount.Amount
		}
		accountData := map[string]any{
			"account_id":  normalized.AccountID,
			"name":        normalized.Name,
			"institution": normalized.Institution,
			"balance":     amount,
			"balances":    normalizedBalances,
		}
		if normalized.Currency != "" {
			accountData["currency"] = normalized.Currency
		}
		if normalized.IBAN != "" {
			accountData["iban"] = normalized.IBAN
		}
		accounts = append(accounts, accountData)
	}

	return map[string]any{
		"session_id": session.SessionID,
		"accounts":   accounts,
		"aspsp":      session.ASPSP,
	}, nil
}

func (p *enableBankingProvider) transactions(e *core.RequestEvent) error {
	if p.opts.Auth.ResolveSession(e, "") == nil {
		return nil
	}
	body := struct {
		AccountID string `json:"accountId"`
		StartDate any    `json:"startDate"`
		ASPSPName string `json:"aspspName"`
	}{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}
	if body.AccountID == "" || body.StartDate == nil {
		return respond(e, struct {
			ErrorCode string `json:"error_code"`
			ErrorType string `json:"error_type"`
		}{"INVALID_INPUT", "Missing accountId or startDate"})
	}

	dateFrom := ""
	switch value := body.StartDate.(type) {
	case string:
		dateFrom = value
	case float64:
		dateFrom = time.UnixMilli(int64(value)).UTC().Format("2006-01-02")
	default:
		dateFrom = fmt.Sprint(value)
	}
	dateTo := time.Now().UTC().Format("2006-01-02")

	headers := dataAccessPSUHeaders(body.ASPSPName, p.psuHeaders(e))
	balances := []enableBankingNormalizedBalance{}
	balanceRaw, err := p.request(http.MethodGet, "/accounts/"+url.PathEscape(body.AccountID)+"/balances", nil, "", headers)
	if err == nil {
		var response struct {
			Balances []enableBankingBalance `json:"balances"`
		}
		if json.Unmarshal(balanceRaw, &response) == nil {
			for _, balance := range response.Balances {
				balances = append(balances, normalizeEnableBankingBalance(balance))
			}
		}
	}
	var startingBalance any
	if len(balances) > 0 {
		preferred := balances[0]
		for _, balance := range balances {
			if balance.BalanceType == "CLAV" {
				preferred = balance
				break
			}
		}
		startingBalance = preferred.BalanceAmount.Amount
	}

	rawTransactions := []enableBankingTransaction{}
	continuation := ""
	for iteration := 0; iteration < 100; iteration++ {
		path := "/accounts/" + url.PathEscape(body.AccountID) + "/transactions?date_from=" + url.QueryEscape(dateFrom) + "&date_to=" + url.QueryEscape(dateTo)
		if continuation != "" {
			path += "&continuation_key=" + url.QueryEscape(continuation)
		}
		raw, err := p.request(http.MethodGet, path, nil, "", headers)
		if err != nil {
			return p.enableBankingWireError(e, err)
		}
		var page struct {
			Transactions    []enableBankingTransaction `json:"transactions"`
			ContinuationKey string                     `json:"continuation_key"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return p.enableBankingWireError(e, err)
		}
		rawTransactions = append(rawTransactions, page.Transactions...)
		if page.ContinuationKey == "" || page.ContinuationKey == continuation {
			break
		}
		continuation = page.ContinuationKey
	}

	all := make([]enableBankingNormalizedTransaction, 0, len(rawTransactions))
	booked := make([]enableBankingNormalizedTransaction, 0, len(rawTransactions))
	pending := make([]enableBankingNormalizedTransaction, 0, len(rawTransactions))
	for _, normalized := range normalizeEnableBankingTransactions(rawTransactions) {
		if !isImportableEnableBankingTransaction(normalized) {
			continue
		}
		all = append(all, normalized)
		if normalized.Booked {
			booked = append(booked, normalized)
		} else {
			pending = append(pending, normalized)
		}
	}

	return respond(e, struct {
		Transactions struct {
			All     []enableBankingNormalizedTransaction `json:"all"`
			Booked  []enableBankingNormalizedTransaction `json:"booked"`
			Pending []enableBankingNormalizedTransaction `json:"pending"`
		} `json:"transactions"`
		Balances        []enableBankingNormalizedBalance `json:"balances"`
		StartingBalance any                              `json:"startingBalance"`
	}{Transactions: struct {
		All     []enableBankingNormalizedTransaction `json:"all"`
		Booked  []enableBankingNormalizedTransaction `json:"booked"`
		Pending []enableBankingNormalizedTransaction `json:"pending"`
	}{all, booked, pending}, Balances: balances, StartingBalance: startingBalance})
}

func (p *enableBankingProvider) enableBankingWireError(e *core.RequestEvent, err error) error {
	var bankError *enableBankingError
	if !errors.As(err, &bankError) {
		return respond(e, struct {
			ErrorType string `json:"error_type"`
			ErrorCode string `json:"error_code"`
		}{"INTERNAL_ERROR", "INTERNAL_ERROR"})
	}
	if bankError.ErrorCode == "INVALID_ACCESS_TOKEN" {
		return respond(e, struct {
			ErrorType string `json:"error_type"`
			ErrorCode string `json:"error_code"`
		}{"ITEM_ERROR", "ITEM_LOGIN_REQUIRED"})
	}
	wireType := bankError.ErrorCode
	if wireType == "NOT_FOUND" {
		wireType = "INVALID_INPUT"
	}
	return respond(e, struct {
		ErrorType string `json:"error_type"`
		ErrorCode string `json:"error_code"`
	}{wireType, bankError.ErrorCode})
}

func (p *enableBankingProvider) psuHeaders(e *core.RequestEvent) map[string]string {
	addr, err := netip.ParseAddr(httpx.ClientIP(e.Request, p.opts.Config.TrustedProxies))
	if err != nil {
		return nil
	}
	ip := addr.Unmap().String()
	if ssrf.IsBlockedHost(ip, ssrf.Options{}) {
		return nil
	}
	headers := map[string]string{"Psu-Ip-Address": ip}
	if userAgent := e.Request.Header.Get("User-Agent"); userAgent != "" {
		headers["Psu-User-Agent"] = userAgent
	}
	return headers
}

func dataAccessPSUHeaders(aspspName string, headers map[string]string) map[string]string {
	if aspspName == "HypoVereinsbank" {
		return nil
	}
	return headers
}

type enableBankingTransaction struct {
	EntryReference    string `json:"entry_reference"`
	TransactionID     string `json:"transaction_id"`
	TransactionAmount struct {
		Currency string `json:"currency"`
		Amount   string `json:"amount"`
	} `json:"transaction_amount"`
	Creditor *struct {
		Name string `json:"name"`
	} `json:"creditor"`
	Debtor *struct {
		Name string `json:"name"`
	} `json:"debtor"`
	CreditDebitIndicator  string   `json:"credit_debit_indicator"`
	Status                string   `json:"status"`
	BookingDate           string   `json:"booking_date"`
	ValueDate             string   `json:"value_date"`
	TransactionDate       string   `json:"transaction_date"`
	RemittanceInformation []string `json:"remittance_information"`
}

type enableBankingNormalizedTransaction struct {
	TransactionID     string `json:"transactionId"`
	Date              string `json:"date"`
	BookingDate       string `json:"bookingDate"`
	ValueDate         string `json:"valueDate,omitempty"`
	TransactionAmount struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"transactionAmount"`
	PayeeName                         string `json:"payeeName"`
	Notes                             string `json:"notes,omitempty"`
	RemittanceInformationUnstructured string `json:"remittanceInformationUnstructured,omitempty"`
	Booked                            bool   `json:"booked"`
}

type enableBankingBalance struct {
	BalanceAmount struct {
		Currency string `json:"currency"`
		Amount   string `json:"amount"`
	} `json:"balance_amount"`
	BalanceType   string `json:"balance_type"`
	ReferenceDate string `json:"reference_date,omitempty"`
}

type enableBankingNormalizedBalance struct {
	BalanceAmount struct {
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
	} `json:"balanceAmount"`
	BalanceType   string `json:"balanceType"`
	ReferenceDate string `json:"referenceDate,omitempty"`
}

type enableBankingSession struct {
	SessionID string `json:"session_id"`
	Accounts  []struct {
		AccountID struct {
			IBAN string `json:"iban"`
		} `json:"account_id"`
		AccountServicer struct {
			BIC  string `json:"bic_fi"`
			Name string `json:"name"`
		} `json:"account_servicer"`
		Name     string `json:"name"`
		Currency string `json:"currency"`
		UID      string `json:"uid"`
	} `json:"accounts"`
	ASPSP struct {
		Name    string `json:"name"`
		Country string `json:"country"`
	} `json:"aspsp"`
}

type enableBankingNormalizedAccount struct {
	AccountID   string
	Name        string
	Institution string
	Currency    string
	IBAN        string
}

func normalizeEnableBankingAccount(account struct {
	AccountID struct {
		IBAN string `json:"iban"`
	} `json:"account_id"`
	AccountServicer struct {
		BIC  string `json:"bic_fi"`
		Name string `json:"name"`
	} `json:"account_servicer"`
	Name     string `json:"name"`
	Currency string `json:"currency"`
	UID      string `json:"uid"`
}, aspsp struct {
	Name    string `json:"name"`
	Country string `json:"country"`
}) enableBankingNormalizedAccount {
	name := account.Name
	if name == "" {
		name = account.AccountID.IBAN
	}
	if name == "" {
		name = account.UID
	}
	institution := aspsp.Name
	if institution == "" {
		institution = account.AccountServicer.Name
	}
	if institution == "" {
		institution = "Unknown"
	}
	return enableBankingNormalizedAccount{
		AccountID:   account.UID,
		Name:        name,
		Institution: institution,
		Currency:    account.Currency,
		IBAN:        account.AccountID.IBAN,
	}
}

var sepaPrefix = regexp.MustCompile(`^(?:EREF|KREF|MREF|CRED|DBTR|CDTR|SVWZ|SVCL|PURP|RTRN|REJT|REFE|SDVA|INDA|NTAV|ULTC|ULTD|ULTB|ABWA|ABWE|IBAN|BIC|COAM|OAMT|REMI|SQTP|ROC)\+`)

func cleanRemittanceLine(value string) string {
	return strings.TrimSpace(sepaPrefix.ReplaceAllString(value, ""))
}

func normalizeEnableBankingTransaction(tx enableBankingTransaction) enableBankingNormalizedTransaction {
	bookingDate := tx.BookingDate
	if bookingDate == "" {
		bookingDate = tx.ValueDate
	}
	if bookingDate == "" {
		bookingDate = tx.TransactionDate
	}

	payee := ""
	switch tx.CreditDebitIndicator {
	case "CRDT":
		if tx.Debtor != nil {
			payee = tx.Debtor.Name
		}
	case "DBIT":
		if tx.Creditor != nil {
			payee = tx.Creditor.Name
		}
	default:
		if tx.Creditor != nil {
			payee = tx.Creditor.Name
		}
		if payee == "" && tx.Debtor != nil {
			payee = tx.Debtor.Name
		}
	}

	cleaned := make([]string, 0, len(tx.RemittanceInformation))
	for _, line := range tx.RemittanceInformation {
		if cleanedLine := cleanRemittanceLine(line); cleanedLine != "" {
			cleaned = append(cleaned, cleanedLine)
		}
	}
	if payee == "" && len(cleaned) > 0 {
		payee = cleaned[0]
	}
	remittance := strings.Join(cleaned, " ")

	amount := strings.TrimSpace(tx.TransactionAmount.Amount)
	if tx.CreditDebitIndicator == "DBIT" {
		amount = "-" + strings.TrimLeft(amount, "+-")
	} else if tx.CreditDebitIndicator == "CRDT" {
		amount = strings.TrimLeft(amount, "+-")
	}

	normalized := enableBankingNormalizedTransaction{
		TransactionID:                     tx.EntryReference,
		Date:                              bookingDate,
		BookingDate:                       bookingDate,
		ValueDate:                         tx.ValueDate,
		PayeeName:                         payee,
		Notes:                             remittance,
		RemittanceInformationUnstructured: remittance,
		Booked:                            tx.Status != "PDNG",
	}
	if normalized.TransactionID == "" {
		normalized.TransactionID = tx.TransactionID
	}
	normalized.TransactionAmount.Amount = amount
	normalized.TransactionAmount.Currency = tx.TransactionAmount.Currency
	return normalized
}

func normalizeEnableBankingTransactions(transactions []enableBankingTransaction) []enableBankingNormalizedTransaction {
	normalized := make([]enableBankingNormalizedTransaction, 0, len(transactions))
	for _, transaction := range transactions {
		normalized = append(normalized, normalizeEnableBankingTransaction(transaction))
	}

	counts := map[string]int{}
	for _, booked := range []bool{true, false} {
		for index := range normalized {
			if normalized[index].TransactionID != "" || normalized[index].Booked != booked || !isImportableEnableBankingTransaction(normalized[index]) {
				continue
			}
			base := deriveEnableBankingID(transactions[index])
			count := counts[base]
			counts[base] = count + 1
			if count == 0 {
				normalized[index].TransactionID = base
			} else {
				normalized[index].TransactionID = base + "-" + strconv.Itoa(count)
			}
		}
	}

	return normalized
}

func deriveEnableBankingID(tx enableBankingTransaction) string {
	var creditor, debtor string
	if tx.Creditor != nil {
		creditor = tx.Creditor.Name
	}
	if tx.Debtor != nil {
		debtor = tx.Debtor.Name
	}

	// Match JSON.stringify on the raw fields used by the Node server. The
	// v2 encoder leaves HTML and Unicode line separators unescaped and
	// encodes a nil remittance slice as [].
	content, _ := jsonv2.Marshal([]any{
		firstNonEmpty(tx.TransactionDate, tx.ValueDate, tx.BookingDate),
		tx.TransactionAmount.Amount,
		tx.TransactionAmount.Currency,
		tx.CreditDebitIndicator,
		creditor,
		debtor,
		tx.RemittanceInformation,
	})
	return "derived-" + hashPrefix(string(content), 32)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func hashPrefix(value string, length int) string {
	sum := sha256.Sum256([]byte(value))
	encoded := hex.EncodeToString(sum[:])
	if len(encoded) > length {
		return encoded[:length]
	}
	return encoded
}

var enableBankingDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func isImportableEnableBankingTransaction(tx enableBankingNormalizedTransaction) bool {
	amount := strings.TrimSpace(tx.TransactionAmount.Amount)
	if amount == "" {
		return false
	}
	if _, err := strconv.ParseFloat(amount, 64); err != nil {
		return false
	}
	return enableBankingDatePattern.MatchString(tx.Date)
}

func normalizeEnableBankingBalance(balance enableBankingBalance) enableBankingNormalizedBalance {
	amount, _ := strconv.ParseFloat(balance.BalanceAmount.Amount, 64)
	normalized := enableBankingNormalizedBalance{
		BalanceType:   balance.BalanceType,
		ReferenceDate: balance.ReferenceDate,
	}
	normalized.BalanceAmount.Amount = int64(math.Round(amount * 100))
	normalized.BalanceAmount.Currency = balance.BalanceAmount.Currency
	return normalized
}
