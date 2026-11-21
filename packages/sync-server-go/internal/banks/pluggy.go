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

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
)

const (
	pluggyClientIDSecret     = "pluggyai_clientId"
	pluggyClientSecretSecret = "pluggyai_clientSecret"
	pluggyItemIDsSecret      = "pluggyai_itemIds"
)

var pluggyBaseURL = "https://api.pluggy.ai"

// SetPluggyBaseURLForTest points the service at a fake API.
func SetPluggyBaseURLForTest(value string) func() {
	previous := pluggyBaseURL
	pluggyBaseURL = strings.TrimRight(value, "/")
	return func() { pluggyBaseURL = previous }
}

type pluggyProvider struct {
	opts   Options
	client *http.Client

	mu     sync.Mutex
	tokens map[pluggyCredentials]string
}

func registerPluggy(actual *router.RouterGroup[*core.RequestEvent], opts Options) {
	provider := &pluggyProvider{
		opts:   opts,
		client: &http.Client{Timeout: 60 * time.Second},
		tokens: map[pluggyCredentials]string{},
	}

	actual.POST("/pluggyai/status", provider.status)
	actual.POST("/pluggyai/accounts", provider.accounts)
	actual.POST("/pluggyai/transactions", provider.transactions)
}

func (p *pluggyProvider) secretSet(fileID string) (bool, error) {
	for _, name := range []string{pluggyClientIDSecret, pluggyClientSecretSecret, pluggyItemIDsSecret} {
		value, exists, err := p.opts.Store.Secret(name, fileID)
		if err != nil {
			return false, err
		}
		if !exists || value == "" {
			return false, nil
		}
	}
	return true, nil
}

func (p *pluggyProvider) credentialSource(fileID string, allowGlobal bool) (string, error) {
	if fileID != "" {
		ok, err := p.secretSet(fileID)
		if err != nil {
			return "", err
		}
		if ok {
			return "per-budget-file", nil
		}
	}
	if allowGlobal {
		ok, err := p.secretSet("")
		if err != nil {
			return "", err
		}
		if ok {
			return "global", nil
		}
	}
	return "", nil
}

type pluggyStatusData struct {
	Configured bool `json:"configured"`
	Source     any  `json:"source"`
}

func (p *pluggyProvider) status(e *core.RequestEvent) error {
	session, _, ok := p.opts.budgetSession(e, true, []string{pluggyClientIDSecret, pluggyClientSecretSecret, pluggyItemIDsSecret})
	if !ok {
		return nil
	}
	fileID := e.Request.Header.Get("X-Actual-File-Id")
	source, err := p.credentialSource(fileID, !session.IsAPIKey)
	if err != nil {
		return bankInternalError(e, err)
	}
	var sourceValue any
	if source != "" {
		sourceValue = source
	}
	return respond(e, pluggyStatusData{Configured: source != "", Source: sourceValue})
}

type pluggyCredentials struct {
	clientID     string
	clientSecret string
}

func (p *pluggyProvider) apiKey(credentials pluggyCredentials) (string, error) {

	p.mu.Lock()
	if token := p.tokens[credentials]; token != "" {
		p.mu.Unlock()
		return token, nil
	}
	p.mu.Unlock()

	body, _ := json.Marshal(map[string]string{"clientId": credentials.clientID, "clientSecret": credentials.clientSecret})
	request, err := http.NewRequest(http.MethodPost, pluggyBaseURL+"/auth", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var decoded struct {
		APIKey string `json:"apiKey"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return "", err
	}
	if response.StatusCode >= 400 || decoded.APIKey == "" {
		return "", fmt.Errorf("pluggy authentication failed")
	}

	p.mu.Lock()
	p.tokens[credentials] = decoded.APIKey
	p.mu.Unlock()
	return decoded.APIKey, nil
}

func (p *pluggyProvider) request(fileID, path string) ([]byte, error) {
	clientID, _, err := p.opts.Store.Secret(pluggyClientIDSecret, fileID)
	if err != nil {
		return nil, err
	}
	clientSecret, _, err := p.opts.Store.Secret(pluggyClientSecretSecret, fileID)
	if err != nil {
		return nil, err
	}
	credentials := pluggyCredentials{clientID: clientID, clientSecret: clientSecret}

	for range 2 {
		apiKey, err := p.apiKey(credentials)
		if err != nil {
			return nil, err
		}
		request, err := http.NewRequest(http.MethodGet, pluggyBaseURL+path, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("X-API-KEY", apiKey)
		response, err := p.client.Do(request)
		if err != nil {
			return nil, err
		}
		if response.StatusCode == http.StatusUnauthorized {
			response.Body.Close()
			p.mu.Lock()
			if p.tokens[credentials] == apiKey {
				delete(p.tokens, credentials)
			}
			p.mu.Unlock()
			continue
		}
		defer response.Body.Close()
		if response.StatusCode >= 400 {
			return nil, fmt.Errorf("pluggy request failed with HTTP %d", response.StatusCode)
		}
		return io.ReadAll(response.Body)
	}
	return nil, fmt.Errorf("pluggy request failed with HTTP %d", http.StatusUnauthorized)
}

func (p *pluggyProvider) accounts(e *core.RequestEvent) error {
	session, _, ok := p.opts.budgetSession(e, true, []string{pluggyClientIDSecret, pluggyClientSecretSecret, pluggyItemIDsSecret})
	if !ok {
		return nil
	}
	fileID := e.Request.Header.Get("X-Actual-File-Id")
	source, err := p.credentialSource(fileID, !session.IsAPIKey)
	if err != nil {
		return bankInternalError(e, err)
	}
	if source == "" {
		return httpx.Fail(e, http.StatusBadRequest, "not-configured")
	}
	credentialFileID := ""
	if source == "per-budget-file" {
		credentialFileID = fileID
	}

	itemIDsRaw, _, _ := p.opts.Store.Secret(pluggyItemIDsSecret, credentialFileID)
	accounts := make([]any, 0)
	for _, item := range strings.Split(itemIDsRaw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		raw, err := p.request(credentialFileID, "/accounts?itemId="+url.QueryEscape(item))
		if err != nil {
			return respond(e, map[string]any{"error": err.Error()})
		}
		var decoded struct {
			Results []any `json:"results"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return respond(e, map[string]any{"error": err.Error()})
		}
		accounts = append(accounts, decoded.Results...)
	}

	return respond(e, struct {
		Accounts []any `json:"accounts"`
	}{Accounts: accounts})
}

func (p *pluggyProvider) transactions(e *core.RequestEvent) error {
	session, _, ok := p.opts.budgetSession(e, true, []string{pluggyClientIDSecret, pluggyClientSecretSecret, pluggyItemIDsSecret})
	if !ok {
		return nil
	}
	fileID := e.Request.Header.Get("X-Actual-File-Id")

	body := struct {
		AccountID string `json:"accountId"`
		StartDate string `json:"startDate"`
	}{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	source, err := p.credentialSource(fileID, !session.IsAPIKey)
	if err != nil {
		return bankInternalError(e, err)
	}
	if source == "" {
		return httpx.Fail(e, http.StatusBadRequest, "not-configured")
	}
	credentialFileID := ""
	if source == "per-budget-file" {
		credentialFileID = fileID
	}

	rawAccount, err := p.request(credentialFileID, "/accounts/"+url.PathEscape(body.AccountID))
	if err != nil {
		return respond(e, map[string]any{"error": err.Error()})
	}
	account := map[string]any{}
	if err := json.Unmarshal(rawAccount, &account); err != nil {
		return respond(e, map[string]any{"error": err.Error()})
	}

	itemID, _ := account["itemId"].(string)
	if session.IsAPIKey {
		itemIDsRaw, _, _ := p.opts.Store.Secret(pluggyItemIDsSecret, fileID)
		allowed := false
		for _, item := range strings.Split(itemIDsRaw, ",") {
			if strings.TrimSpace(item) == itemID {
				allowed = true
				break
			}
		}
		if !allowed {
			return httpx.Fail(e, http.StatusForbidden, "file-access-denied")
		}
	}

	owner, _ := account["owner"].(string)
	startDate := body.StartDate
	sandbox := owner == "John Doe"
	if sandbox {
		startDate = "2000-01-01"
	}

	rawTransactions, err := p.fetchAllTransactions(credentialFileID, body.AccountID, startDate)
	if err != nil {
		return respond(e, map[string]any{"error": err.Error()})
	}

	balance, _ := account["balance"].(float64)
	startingBalance := int64(math.Round(balance * 100))
	accountType, _ := account["type"].(string)
	if accountType == "CREDIT" {
		startingBalance = -startingBalance
	}
	updatedAt, _ := account["updatedAt"].(string)
	date := pluggyDate(parsePluggyTime(updatedAt))
	currencyCode, _ := account["currencyCode"].(string)

	balances := []map[string]any{{
		"balanceAmount": map[string]any{"amount": startingBalance, "currency": currencyCode},
		"balanceType":   "expected",
		"referenceDate": date,
	}}

	all := make([]map[string]any, 0, len(rawTransactions))
	booked := make([]map[string]any, 0, len(rawTransactions))
	pending := make([]map[string]any, 0, len(rawTransactions))

	for _, transaction := range rawTransactions {
		if len(transaction) == 0 {
			continue
		}
		status, _ := transaction["status"].(string)
		transactionDate := parsePluggyTime(stringValue(transaction["date"]))
		if transactionDate.Before(parsePluggyTime(startDate)) && transaction["sandbox"] != true {
			continue
		}

		bookedTransaction := status != "PENDING"
		normalized := map[string]any{}
		normalized["booked"] = bookedTransaction
		normalized["date"] = pluggyDate(transactionDate)
		normalized["payeeName"] = pluggyPayeeName(transaction)
		notes := stringValue(transaction["descriptionRaw"])
		if notes == "" {
			notes = stringValue(transaction["description"])
		}
		normalized["notes"] = notes

		amount := numberValue(transaction["amountInAccountCurrency"])
		if transaction["amountInAccountCurrency"] == nil {
			amount = numberValue(transaction["amount"])
		}
		if accountType == "CREDIT" {
			amount = -amount
		}
		amount = math.Round(amount*100) / 100
		transactionCurrency := stringValue(transaction["currencyCode"])
		normalized["transactionAmount"] = map[string]any{"amount": amount, "currency": transactionCurrency}
		normalized["transactionId"] = stringValue(transaction["id"])
		normalized["sortOrder"] = transactionDate.UnixMilli()
		normalized["originalDate"] = pluggyDate(transactionDate)
		normalized["date"] = pluggyDate(pluggyCorrectedDate(transaction, transactionDate))

		delete(transaction, "amount")
		final := pluggyFlatten(transaction, "")
		for key, value := range normalized {
			final[key] = value
		}

		if bookedTransaction {
			booked = append(booked, final)
		} else {
			pending = append(pending, final)
		}
		all = append(all, final)
	}

	sortPluggy := func(rows []map[string]any) {
		sort.SliceStable(rows, func(i, j int) bool {
			return numberValue(rows[i]["sortOrder"]) > numberValue(rows[j]["sortOrder"])
		})
	}
	sortPluggy(all)
	sortPluggy(booked)
	sortPluggy(pending)

	return respond(e, map[string]any{
		"balances":        balances,
		"startingBalance": startingBalance,
		"transactions": map[string]any{
			"all":     all,
			"booked":  booked,
			"pending": pending,
		},
	})
}

func (p *pluggyProvider) fetchAllTransactions(fileID, accountID, dateFrom string) ([]map[string]any, error) {
	result := []map[string]any{}
	query := url.Values{"accountId": {accountID}, "dateFrom": {dateFrom}}
	seen := map[string]bool{}
	for {
		raw, err := p.request(fileID, "/v2/transactions?"+query.Encode())
		if err != nil {
			return nil, err
		}
		var page struct {
			Results []map[string]any `json:"results"`
			Next    string           `json:"next"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		result = append(result, page.Results...)
		next, err := url.Parse(page.Next)
		if err != nil {
			return nil, fmt.Errorf("invalid Pluggy pagination link: %w", err)
		}
		after := next.Query().Get("after")
		if after == "" {
			return result, nil
		}
		if seen[after] {
			return nil, fmt.Errorf("repeated Pluggy pagination cursor")
		}
		seen[after] = true
		query.Set("after", after)
	}
}

func pluggyPayeeName(transaction map[string]any) string {
	if merchant, ok := transaction["merchant"].(map[string]any); ok {
		if name := stringValue(merchant["name"]); name != "" {
			return name
		}
		if name := stringValue(merchant["businessName"]); name != "" {
			return name
		}
	}
	if payment, ok := transaction["paymentData"].(map[string]any); ok {
		kind := stringValue(transaction["type"])
		if kind == "DEBIT" {
			if receiver, ok := payment["receiver"].(map[string]any); ok {
				if name := stringValue(receiver["name"]); name != "" {
					return name
				}
				if document, ok := receiver["documentNumber"].(map[string]any); ok {
					return stringValue(document["value"])
				}
			}
		}
		if kind == "CREDIT" {
			if payer, ok := payment["payer"].(map[string]any); ok {
				if name := stringValue(payer["name"]); name != "" {
					return name
				}
				if document, ok := payer["documentNumber"].(map[string]any); ok {
					return stringValue(document["value"])
				}
			}
		}
	}
	return ""
}

func pluggyCorrectedDate(transaction map[string]any, date time.Time) time.Time {
	metadata, ok := transaction["creditCardMetadata"].(map[string]any)
	if !ok || metadata["installmentNumber"] == nil {
		return date
	}
	installment := int(numberValue(metadata["installmentNumber"]))
	purchase := date
	if raw := stringValue(metadata["purchaseDate"]); raw != "" {
		purchase = parsePluggyTime(raw)
	}
	return addMonthsClamped(purchase, installment-1)
}

func addMonthsClamped(date time.Time, months int) time.Time {
	year, month, day := date.Date()
	target := time.Date(year, month+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	lastDay := time.Date(target.Year(), target.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(target.Year(), target.Month(), day, 0, 0, 0, 0, time.UTC)
}

func pluggyDate(date time.Time) string {
	return date.UTC().Format("2006-01-02")
}

func parsePluggyTime(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		parsed, err = time.Parse("2006-01-02", raw)
		if err != nil {
			return time.Time{}
		}
	}
	return parsed
}

func pluggyFlatten(value map[string]any, prefix string) map[string]any {
	result := map[string]any{}
	for key, item := range value {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}
		if item == nil {
			continue
		}
		if nested, ok := item.(map[string]any); ok {
			for nestedKey, nestedValue := range pluggyFlatten(nested, fullKey) {
				result[nestedKey] = nestedValue
			}
		} else {
			result[fullKey] = item
		}
	}
	return result
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		result, _ := typed.Float64()
		return result
	default:
		return 0
	}
}
