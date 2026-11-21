package banks

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
)

const (
	akahuUserTokenSecret = "akahu_userToken"
	akahuAppTokenSecret  = "akahu_appToken"
)

var (
	akahuBaseURL     = "https://api.akahu.io/v1"
	akahuRefreshWait = 3 * time.Second
)

// SetAkahuRefreshWaitForTest shortens the refresh polling delay.
func SetAkahuRefreshWaitForTest(value time.Duration) func() {
	previous := akahuRefreshWait
	akahuRefreshWait = value
	return func() { akahuRefreshWait = previous }
}

// SetAkahuBaseURLForTest points the service at a fake API.
func SetAkahuBaseURLForTest(value string) func() {
	previous := akahuBaseURL
	akahuBaseURL = strings.TrimRight(value, "/")
	return func() { akahuBaseURL = previous }
}

type akahuProvider struct {
	opts   Options
	client *http.Client
	mu     sync.Mutex
}

func registerAkahu(actual *router.RouterGroup[*core.RequestEvent], opts Options) {
	provider := &akahuProvider{opts: opts, client: &http.Client{Timeout: 60 * time.Second}}
	actual.POST("/akahu/status", provider.status)
	actual.POST("/akahu/accounts", provider.accounts)
	actual.POST("/akahu/transactions", provider.transactions)
}

func (p *akahuProvider) tokens() (string, string, bool) {
	userToken, userExists, _ := p.opts.Store.Secret(akahuUserTokenSecret, "")
	appToken, appExists, _ := p.opts.Store.Secret(akahuAppTokenSecret, "")
	return userToken, appToken, userExists && appExists
}

func (p *akahuProvider) status(e *core.RequestEvent) error {
	if p.opts.Auth.ResolveSession(e, "") == nil {
		return nil
	}
	_, _, configured := p.tokens()
	return respond(e, struct {
		Configured bool `json:"configured"`
	}{Configured: configured})
}

func (p *akahuProvider) request(userToken, appToken, method, path string) (map[string]any, error) {
	request, err := http.NewRequest(method, akahuBaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+userToken)
	request.Header.Set("X-Akahu-Id", appToken)
	request.Header.Set("User-Agent", "actual-server")

	response, err := p.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	success, _ := decoded["success"].(bool)
	if response.StatusCode >= 400 || !success {
		message, _ := decoded["message"].(string)
		if message == "" {
			message = fmt.Sprintf("Akahu request failed with HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("%s", message)
	}
	return decoded, nil
}

func akahuItems(decoded map[string]any) []any {
	if items, ok := decoded["items"].([]any); ok {
		return items
	}
	if item, ok := decoded["item"].(map[string]any); ok {
		return []any{item}
	}
	return nil
}

func (p *akahuProvider) accounts(e *core.RequestEvent) error {
	if p.opts.Auth.ResolveSession(e, "") == nil {
		return nil
	}
	userToken, appToken, configured := p.tokens()
	if !configured {
		return respond(e, map[string]any{"error": "Missing user or app token"})
	}

	decoded, err := p.request(userToken, appToken, http.MethodGet, "/accounts")
	if err != nil {
		return akahuError(e, err)
	}
	accounts := akahuItems(decoded)
	if accounts == nil {
		accounts = []any{}
	}
	return respond(e, struct {
		Accounts []any `json:"accounts"`
	}{Accounts: accounts})
}

func akahuError(e *core.RequestEvent, err error) error {
	return respondStatus(e, "error", map[string]any{"error": err.Error()})
}

func (p *akahuProvider) transactions(e *core.RequestEvent) error {
	if p.opts.Auth.ResolveSession(e, "") == nil {
		return nil
	}
	body := struct {
		AccountID string `json:"accountId"`
		StartDate string `json:"startDate"`
	}{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}
	if body.AccountID == "" || body.StartDate == "" {
		return respondStatus(e, "error", map[string]any{"error": "accountId and startDate are required"})
	}

	userToken, appToken, configured := p.tokens()
	if !configured {
		return respond(e, map[string]any{"error": "Missing user or app token"})
	}

	account, err := p.refreshedAccount(userToken, appToken, body.AccountID)
	if err != nil {
		return akahuError(e, fmt.Errorf("Failed to fetch transactions: %w", err))
	}
	if account == nil {
		return respondStatus(e, "error", map[string]any{"error": "Account not found"})
	}

	balance, _ := account["balance"].(map[string]any)
	current, hasCurrent := numberValueOK(balance["current"])
	currency, _ := balance["currency"].(string)
	if currency == "" {
		currency = "NZD"
	}
	if !hasCurrent {
		return respondStatus(e, "error", map[string]any{"error": "Account balance unavailable"})
	}

	now := time.Now()
	startDate, err := akahuStartDate(body.StartDate)
	if err != nil {
		return respondStatus(e, "error", map[string]any{"error": "accountId and startDate are required"})
	}
	if startDate.After(now) {
		startDate = now
	}
	endDate := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.Local)

	transactions := []any{}
	cursor := ""
	for {
		query := url.Values{}
		query.Set("start", startDate.Add(-time.Millisecond).UTC().Format(time.RFC3339Nano))
		query.Set("end", endDate.UTC().Format(time.RFC3339Nano))
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		decoded, err := p.request(userToken, appToken, http.MethodGet,
			"/accounts/"+url.PathEscape(body.AccountID)+"/transactions?"+query.Encode())
		if err != nil {
			return akahuError(e, fmt.Errorf("Failed to fetch transactions: %w", err))
		}
		transactions = append(transactions, akahuItems(decoded)...)
		cursor = ""
		if next, ok := decoded["cursor"].(map[string]any); ok {
			cursor, _ = next["next"].(string)
		}
		if cursor == "" {
			break
		}
	}

	pendingRaw, err := p.request(userToken, appToken, http.MethodGet,
		"/accounts/"+url.PathEscape(body.AccountID)+"/transactions/pending")
	if err != nil {
		return akahuError(e, fmt.Errorf("Failed to fetch transactions: %w", err))
	}
	pendingTransactions := akahuItems(pendingRaw)

	referenceDate := akahuNZDate(now)
	if refreshed, ok := account["refreshed"].(map[string]any); ok {
		if raw, ok := refreshed["balance"].(string); ok {
			if parsed := parsePluggyTime(raw); !parsed.IsZero() {
				referenceDate = akahuNZDate(parsed)
			}
		}
	}
	currentBalance := int64(math.Round(current * 100))
	balances := []map[string]any{{
		"balanceAmount": map[string]any{"amount": currentBalance, "currency": currency},
		"balanceType":   "expected",
		"referenceDate": referenceDate,
	}}
	if available, ok := numberValueOK(balance["available"]); ok {
		balances = append(balances, map[string]any{
			"balanceAmount": map[string]any{"amount": int64(math.Round(available * 100)), "currency": currency},
			"balanceType":   "interimAvailable",
			"referenceDate": referenceDate,
		})
	}

	all := []map[string]any{}
	booked := []map[string]any{}
	pending := []map[string]any{}
	for _, raw := range transactions {
		transaction, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		transactionDate := parsePluggyTime(stringValue(transaction["date"]))
		if !transactionDate.IsZero() && !transactionDate.Before(startDate) {
			processed := p.processAkahuTransaction(transaction, currency, true)
			booked = append(booked, processed)
			all = append(all, processed)
		}
	}
	for _, raw := range pendingTransactions {
		transaction, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		transactionDate := parsePluggyTime(stringValue(transaction["date"]))
		if !transactionDate.IsZero() && !transactionDate.Before(startDate) {
			processed := p.processAkahuTransaction(transaction, currency, false)
			pending = append(pending, processed)
			all = append(all, processed)
		}
	}

	sortAkahu := func(rows []map[string]any) {
		sort.SliceStable(rows, func(i, j int) bool {
			return numberValue(rows[i]["sortOrder"]) > numberValue(rows[j]["sortOrder"])
		})
	}
	sortAkahu(all)
	sortAkahu(booked)
	sortAkahu(pending)

	return respond(e, map[string]any{
		"balances":        balances,
		"startingBalance": currentBalance,
		"transactions": map[string]any{
			"all":     all,
			"booked":  booked,
			"pending": pending,
		},
	})
}

func (p *akahuProvider) processAkahuTransaction(transaction map[string]any, currency string, booked bool) map[string]any {
	transactionDate := parsePluggyTime(stringValue(transaction["date"]))
	payee := ""
	if merchant, ok := transaction["merchant"].(map[string]any); ok {
		payee = stringValue(merchant["name"])
	}
	if payee == "" {
		if meta, ok := transaction["meta"].(map[string]any); ok {
			payee = stringValue(meta["other_account"])
		}
	}
	if payee == "" {
		payee = stringValue(transaction["description"])
	}

	result := map[string]any{}
	for key, value := range transaction {
		result[key] = value
	}
	result["booked"] = booked
	result["date"] = akahuNZDate(transactionDate)
	result["payeeName"] = payee
	result["sortOrder"] = transactionDate.UnixMilli()
	result["notes"] = stringValue(transaction["description"])
	result["transactionAmount"] = map[string]any{
		"amount":   math.Round(numberValue(transaction["amount"])*100) / 100,
		"currency": currency,
	}
	if booked {
		result["transactionId"] = stringValue(transaction["_id"])
	}
	if merchant, ok := transaction["merchant"].(map[string]any); ok {
		result["merchant"] = merchant
	} else {
		other := ""
		if meta, ok := transaction["meta"].(map[string]any); ok {
			other = stringValue(meta["other_account"])
		}
		result["merchant"] = map[string]any{"name": other}
	}
	if category, ok := transaction["category"].(map[string]any); ok {
		result["category"] = stringValue(category["name"])
	}
	return result
}

func (p *akahuProvider) refreshedAccount(userToken, appToken, accountID string) (map[string]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	decoded, err := p.request(userToken, appToken, http.MethodGet, "/accounts/"+url.PathEscape(accountID))
	if err != nil {
		return nil, err
	}
	account, _ := decoded["item"].(map[string]any)
	if account == nil {
		return nil, nil
	}
	if !akahuNeedsRefresh(account) {
		return account, nil
	}

	if _, err := p.request(userToken, appToken, http.MethodPost, "/refresh"); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		time.Sleep(akahuRefreshWait)
		decoded, err = p.request(userToken, appToken, http.MethodGet, "/accounts/"+url.PathEscape(accountID))
		if err != nil {
			return nil, err
		}
		account, _ = decoded["item"].(map[string]any)
		if account == nil || !akahuNeedsRefresh(account) {
			time.Sleep(akahuRefreshWait)
			break
		}
	}
	return account, nil
}

func akahuNeedsRefresh(account map[string]any) bool {
	refreshed, ok := account["refreshed"].(map[string]any)
	if !ok {
		return false
	}
	raw, _ := refreshed["transactions"].(string)
	if raw == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return false
	}
	return time.Since(parsed) > time.Hour
}

var auckland = func() *time.Location {
	location, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		return time.UTC
	}
	return location
}()

func akahuStartDate(raw string) (time.Time, error) {
	if len(raw) == 10 && raw[4] == '-' && raw[7] == '-' {
		parsed, err := time.ParseInLocation("2006-01-02", raw, auckland)
		if err != nil {
			return time.Time{}, err
		}
		return parsed, nil
	}
	return time.Parse(time.RFC3339, raw)
}

func akahuNZDate(date time.Time) string {
	return date.In(auckland).Format("2006-01-02")
}

func numberValueOK(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
