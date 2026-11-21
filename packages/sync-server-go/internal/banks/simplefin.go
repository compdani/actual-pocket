package banks

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/ssrf"
)

const (
	simplefinTokenSecret     = "simplefin_token"
	simplefinAccessKeySecret = "simplefin_accessKey"
)

var simplefinAccessKeyPattern = regexp.MustCompile(`^.*//.*:.*@.*$`)

type simplefinProvider struct {
	opts     Options
	client   *http.Client
	override *url.URL
	allowed  []netip.Prefix
}

func registerSimpleFIN(actual *router.RouterGroup[*core.RequestEvent], opts Options) {
	allowed := append([]netip.Prefix{}, opts.Config.SimpleFINAllowedNetworks...)
	allowed = append(allowed, opts.Test.SimpleFINAllowedNets...)
	provider := &simplefinProvider{
		opts:     opts,
		override: opts.Test.SimpleFINBaseURL,
		allowed:  allowed,
		client: &http.Client{
			Transport: ssrf.NewTransport(ssrf.Options{AllowedNetworks: allowed}),
			Timeout:   60 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}

	actual.POST("/simplefin/status", provider.status)
	actual.POST("/simplefin/accounts", provider.accounts)
	actual.POST("/simplefin/transactions", provider.transactions)
}

type simplefinStatusData struct {
	Configured bool `json:"configured"`
	Source     any  `json:"source"`
}

func (p *simplefinProvider) status(e *core.RequestEvent) error {
	_, credentialFileID, ok := p.opts.budgetSession(e, true, []string{simplefinTokenSecret, simplefinAccessKeySecret})
	if !ok {
		return nil
	}

	token, exists, err := p.opts.Store.Secret(simplefinTokenSecret, credentialFileID)
	if err != nil {
		return bankInternalError(e, err)
	}

	configured := exists && !isSimpleFINForbidden(token)
	var source any
	if credentialFileID != "" {
		source = "per-budget-file"
	} else if configured {
		source = "global"
	}

	return respond(e, simplefinStatusData{Configured: configured, Source: source})
}

type simplefinInvalidTokenData struct {
	ErrorType string `json:"error_type"`
	ErrorCode string `json:"error_code"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
}

func simplefinInvalidToken(e *core.RequestEvent) error {
	return respond(e, simplefinInvalidTokenData{
		ErrorType: "INVALID_ACCESS_TOKEN",
		ErrorCode: "INVALID_ACCESS_TOKEN",
		Status:    "rejected",
		Reason:    "Invalid SimpleFIN access token.  Reset the token and re-link any broken accounts.",
	})
}

func simplefinServerDown(e *core.RequestEvent) error {
	return respond(e, simplefinInvalidTokenData{
		ErrorType: "SERVER_DOWN",
		ErrorCode: "SERVER_DOWN",
		Status:    "rejected",
		Reason:    "There was an error communicating with SimpleFIN.",
	})
}

func (p *simplefinProvider) accounts(e *core.RequestEvent) error {
	_, credentialFileID, ok := p.opts.budgetSession(e, true, []string{simplefinTokenSecret, simplefinAccessKeySecret})
	if !ok {
		return nil
	}

	accessKey, err := secretOrEmpty(p.opts, simplefinAccessKeySecret, credentialFileID)
	if err != nil {
		return bankInternalError(e, err)
	}

	if isInvalidSimpleFINAccessKey(accessKey) {
		token, err := secretOrEmpty(p.opts, simplefinTokenSecret, credentialFileID)
		if err != nil {
			return bankInternalError(e, err)
		}
		if token == "" || isSimpleFINForbidden(token) {
			return simplefinInvalidToken(e)
		}

		claimURL, ok := decodeSimpleFINClaimURL(token)
		if !ok {
			return simplefinInvalidToken(e)
		}

		accessKey, err = p.claimAccessKey(claimURL)
		if err != nil {
			return simplefinServerDown(e)
		}
		if isInvalidSimpleFINAccessKey(accessKey) {
			sensitiveLogf("actual-sensitive:simplefin", "SimpleFIN rejected the setup token claim: %s", truncate(accessKey, 200))
			return simplefinInvalidToken(e)
		}

		if err := p.opts.Store.SetSecret(simplefinAccessKeySecret, credentialFileID, accessKey); err != nil {
			return bankInternalError(e, err)
		}
	}

	results, err := p.getAccounts(accessKey, nil, time.Time{}, time.Time{}, true)
	if err != nil {
		return simplefinServerDown(e)
	}

	return respond(e, struct {
		Accounts []json.RawMessage `json:"accounts"`
	}{Accounts: results.RawAccounts})
}

type simplefinTransactionsRequest struct {
	AccountID json.RawMessage `json:"accountId"`
	StartDate json.RawMessage `json:"startDate"`
}

func (p *simplefinProvider) transactions(e *core.RequestEvent) error {
	_, credentialFileID, ok := p.opts.budgetSession(e, true, []string{simplefinTokenSecret, simplefinAccessKeySecret})
	if !ok {
		return nil
	}

	body := simplefinTransactionsRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	accountIDs, accountIsArray, err := parseSimpleFINStrings(body.AccountID)
	if err != nil {
		return bankInternalError(e, err)
	}
	startDates, startIsArray, err := parseSimpleFINStrings(body.StartDate)
	if err != nil {
		return bankInternalError(e, err)
	}
	if len(accountIDs) == 0 || len(startDates) == 0 {
		return bankInternalError(e, errors.New("accountId and startDate are required"))
	}
	if accountIsArray != startIsArray {
		return bankInternalError(e, errors.New("accountId and startDate must either both be arrays or both be strings"))
	}
	if accountIsArray && len(accountIDs) != len(startDates) {
		return bankInternalError(e, errors.New("accountId and startDate arrays must be the same length"))
	}

	accessKey, err := secretOrEmpty(p.opts, simplefinAccessKeySecret, credentialFileID)
	if err != nil {
		return bankInternalError(e, err)
	}
	if isInvalidSimpleFINAccessKey(accessKey) {
		return simplefinInvalidToken(e)
	}

	earliest := startDates[0]
	for _, date := range startDates {
		if date < earliest {
			earliest = date
		}
	}
	startDate := parseSimpleFINDate(earliest)

	results, err := p.getTransactions(accessKey, accountIDs, startDate)
	if err != nil {
		if errors.Is(err, errSimpleFINForbidden) {
			return simplefinInvalidToken(e)
		}
		return simplefinServerDown(e)
	}

	if !accountIsArray {
		accountID := ""
		if len(accountIDs) > 0 {
			accountID = accountIDs[0]
		}
		response := p.getAccountResponse(results, accountID, startDate)
		if results.HasError {
			if errors := results.Errors[accountID]; len(errors) > 0 {
				return respond(e, errors[0])
			}
		}
		return respond(e, response)
	}

	response := make(map[string]any, len(accountIDs)+1)
	for index, accountID := range accountIDs {
		if account := p.getAccountResponse(results, accountID, parseSimpleFINDate(startDates[index])); account != nil {
			response[accountID] = account
		}
	}
	if results.HasError {
		response["errors"] = results.Errors
	}

	return respond(e, response)
}

type simplefinTransaction struct {
	ID           string `json:"id"`
	Payee        string `json:"payee"`
	Description  string `json:"description"`
	Amount       string `json:"amount"`
	TransactedAt int64  `json:"transacted_at"`
	Posted       int64  `json:"posted"`
	Pending      *bool  `json:"pending"`
}

type simplefinAccount struct {
	ID  string `json:"id"`
	Org struct {
		Name string `json:"name"`
	} `json:"org"`
	Balance      string                 `json:"balance"`
	BalanceDate  int64                  `json:"balance-date"`
	Currency     string                 `json:"currency"`
	Transactions []simplefinTransaction `json:"transactions"`
}

type simplefinProviderError struct {
	ErrorType string `json:"error_type"`
	ErrorCode string `json:"error_code"`
	Reason    string `json:"reason"`
}

type simplefinResults struct {
	Accounts []simplefinAccount `json:"accounts"`
	SFErrors []string           `json:"errors"`

	RawAccounts []json.RawMessage `json:"-"`
	Errors      map[string][]simplefinProviderError
	HasError    bool
}

type simplefinBalance struct {
	BalanceAmount struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"balanceAmount"`
	BalanceType   string `json:"balanceType"`
	ReferenceDate string `json:"referenceDate"`
}

type simplefinNormalizedTransaction struct {
	Booked            bool   `json:"booked"`
	SortOrder         int64  `json:"sortOrder"`
	Date              string `json:"date"`
	PayeeName         string `json:"payeeName,omitempty"`
	Notes             string `json:"notes,omitempty"`
	TransactionAmount struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"transactionAmount"`
	TransactionID  string `json:"transactionId"`
	TransactedDate string `json:"transactedDate,omitempty"`
	PostedDate     string `json:"postedDate,omitempty"`
}

type simplefinAccountResponse struct {
	Balances        []simplefinBalance `json:"balances"`
	StartingBalance int                `json:"startingBalance"`
	Transactions    struct {
		All     []simplefinNormalizedTransaction `json:"all"`
		Booked  []simplefinNormalizedTransaction `json:"booked"`
		Pending []simplefinNormalizedTransaction `json:"pending"`
	} `json:"transactions"`
}

func (p *simplefinProvider) getAccountResponse(results *simplefinResults, accountID string, startDate time.Time) *simplefinAccountResponse {
	var account *simplefinAccount
	for index := range results.Accounts {
		if results.Accounts[index].ID == accountID {
			account = &results.Accounts[index]
			break
		}
	}

	if account == nil {
		p.logAccountError(results, accountID, simplefinProviderError{
			ErrorType: "ACCOUNT_MISSING",
			ErrorCode: "ACCOUNT_MISSING",
			Reason:    fmt.Sprintf(`The account "%s" was not found. Try unlinking and relinking the account.`, accountID),
		})
		return nil
	}

	for _, message := range results.SFErrors {
		if strings.HasPrefix(message, fmt.Sprintf("Connection to %s may need attention", account.Org.Name)) {
			p.logAccountError(results, accountID, simplefinProviderError{
				ErrorType: "ACCOUNT_NEEDS_ATTENTION",
				ErrorCode: "ACCOUNT_NEEDS_ATTENTION",
				Reason:    `The account needs your attention at <a href="https://bridge.simplefin.org/auth/login">SimpleFIN</a>.`,
			})
			break
		}
	}

	startingBalance, _ := strconv.Atoi(strings.Replace(account.Balance, ".", "", 1))
	date := simpleFINDate(time.Unix(account.BalanceDate, 0))

	balances := []simplefinBalance{
		simplefinBalance{
			BalanceAmount: struct {
				Amount   string `json:"amount"`
				Currency string `json:"currency"`
			}{Amount: account.Balance, Currency: account.Currency},
			BalanceType:   "expected",
			ReferenceDate: date,
		},
		simplefinBalance{
			BalanceAmount: struct {
				Amount   string `json:"amount"`
				Currency string `json:"currency"`
			}{Amount: account.Balance, Currency: account.Currency},
			BalanceType:   "interimAvailable",
			ReferenceDate: date,
		},
	}

	all := make([]simplefinNormalizedTransaction, 0, len(account.Transactions))
	booked := make([]simplefinNormalizedTransaction, 0, len(account.Transactions))
	pending := make([]simplefinNormalizedTransaction, 0, len(account.Transactions))

	for _, transaction := range account.Transactions {
		isPending := transaction.Posted == 0
		if transaction.Pending != nil {
			isPending = *transaction.Pending
		}

		dateToUse := transaction.Posted
		if isPending {
			dateToUse = transaction.TransactedAt
		}

		transactionDate := time.Unix(dateToUse, 0)
		if transactionDate.Before(startDate) {
			continue
		}

		normalized := simplefinNormalizedTransaction{
			Booked:        !isPending,
			SortOrder:     dateToUse,
			Date:          simpleFINDate(transactionDate),
			PayeeName:     transaction.Payee,
			Notes:         transaction.Description,
			TransactionID: transaction.ID,
		}
		normalized.TransactionAmount.Amount = transaction.Amount
		normalized.TransactionAmount.Currency = "USD"

		if transaction.TransactedAt != 0 {
			normalized.TransactedDate = simpleFINDate(time.Unix(transaction.TransactedAt, 0))
		}
		if transaction.Posted != 0 {
			normalized.PostedDate = simpleFINDate(time.Unix(transaction.Posted, 0))
		}

		if normalized.Booked {
			booked = append(booked, normalized)
		} else {
			pending = append(pending, normalized)
		}
		all = append(all, normalized)
	}

	sortTransactions := func(rows []simplefinNormalizedTransaction) {
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].SortOrder > rows[j].SortOrder })
	}
	sortTransactions(all)
	sortTransactions(booked)
	sortTransactions(pending)

	response := &simplefinAccountResponse{Balances: balances, StartingBalance: startingBalance}
	response.Transactions.All = all
	response.Transactions.Booked = booked
	response.Transactions.Pending = pending
	return response
}

func (p *simplefinProvider) logAccountError(results *simplefinResults, accountID string, providerError simplefinProviderError) {
	if results.Errors == nil {
		results.Errors = map[string][]simplefinProviderError{}
	}
	results.Errors[accountID] = append(results.Errors[accountID], providerError)
	results.HasError = true
}

func simpleFINDate(value time.Time) string {
	return value.UTC().Format("2006-01-02")
}

func parseSimpleFINDate(raw string) time.Time {
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func parseSimpleFINStrings(raw json.RawMessage) ([]string, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}

	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, false, nil
	}

	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, true, nil
	}

	return nil, false, errors.New("accountId and startDate must be strings or arrays of strings")
}

// rewriteURL points a bank URL at the test override origin, preserving its
// path and credentials.
func (p *simplefinProvider) rewriteURL(raw string) string {
	if p.override == nil {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.Scheme = p.override.Scheme
	parsed.Host = p.override.Host
	return parsed.String()
}

var errSimpleFINForbidden = errors.New("Forbidden")

func isSimpleFINForbidden(value string) bool {
	return strings.HasPrefix(value, "Forbidden")
}

func isInvalidSimpleFINAccessKey(accessKey string) bool {
	return isSimpleFINForbidden(accessKey) || !simplefinAccessKeyPattern.MatchString(accessKey)
}

func decodeSimpleFINClaimURL(token string) (string, bool) {
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", false
	}
	raw := string(decoded)
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", false
	}
	return raw, true
}

func (p *simplefinProvider) claimAccessKey(claimURL string) (string, error) {
	parsed, err := url.Parse(claimURL)
	if err != nil {
		return "", err
	}
	claimURL = p.rewriteURL(claimURL)
	parsed, err = url.Parse(claimURL)
	if err != nil {
		return "", err
	}
	if ssrf.IsBlockedHost(parsed.Hostname(), ssrf.Options{AllowedNetworks: p.allowed}) {
		return "", ssrf.ErrBlocked
	}

	request, err := http.NewRequest(http.MethodPost, claimURL, nil)
	if err != nil {
		return "", err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	if (response.StatusCode < 200 || response.StatusCode >= 300) && response.StatusCode != http.StatusForbidden {
		return "", fmt.Errorf("SimpleFIN claim failed with HTTP %d", response.StatusCode)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

type simplefinAccessKey struct {
	baseURL  string
	username string
	password string
}

func parseSimpleFINAccessKey(accessKey string) (simplefinAccessKey, error) {
	if !simplefinAccessKeyPattern.MatchString(accessKey) {
		return simplefinAccessKey{}, errors.New("invalid access key")
	}

	scheme, rest, _ := strings.Cut(accessKey, "//")
	auth, rest, _ := strings.Cut(rest, "@")
	username, password, _ := strings.Cut(auth, ":")

	return simplefinAccessKey{
		baseURL:  scheme + "//" + rest,
		username: username,
		password: password,
	}, nil
}

func (p *simplefinProvider) getTransactions(accessKey string, accounts []string, startDate time.Time) (*simplefinResults, error) {
	now := time.Now()
	if startDate.IsZero() {
		startDate = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	endDate := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return p.getAccounts(accessKey, accounts, startDate, endDate, false)
}

func (p *simplefinProvider) getAccounts(accessKey string, accounts []string, startDate, endDate time.Time, noTransactions bool) (*simplefinResults, error) {
	sfin, err := parseSimpleFINAccessKey(accessKey)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	if !noTransactions {
		if !startDate.IsZero() {
			query.Set("start-date", strconv.FormatInt(startDate.Unix(), 10))
		}
		if !endDate.IsZero() {
			query.Set("end-date", strconv.FormatInt(endDate.Unix(), 10))
		}
		query.Set("pending", "1")
	} else {
		query.Set("balances-only", "1")
	}
	for _, account := range accounts {
		query.Add("account", account)
	}

	target := strings.TrimRight(p.rewriteURL(sfin.baseURL), "/") + "/accounts?" + query.Encode()
	current, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	authorization := "Basic " + base64.StdEncoding.EncodeToString([]byte(sfin.username+":"+sfin.password))
	const maxRedirects = 5

	var response *http.Response
	for hop := 0; ; hop++ {
		if ssrf.IsBlockedHost(current.Hostname(), ssrf.Options{AllowedNetworks: p.allowed}) {
			return nil, ssrf.ErrBlocked
		}

		request, err := http.NewRequest(http.MethodGet, current.String(), nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", authorization)

		response, err = p.client.Do(request)
		if err != nil {
			return nil, err
		}

		location := response.Header.Get("Location")
		if response.StatusCode < 300 || response.StatusCode >= 400 || location == "" {
			break
		}
		response.Body.Close()

		if hop >= maxRedirects {
			return nil, errors.New("too many redirects")
		}

		next, err := current.Parse(location)
		if err != nil {
			return nil, err
		}
		if next.Scheme != current.Scheme || next.Host != current.Host {
			authorization = ""
		}
		current = next
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusForbidden {
		return nil, errSimpleFINForbidden
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	results := &simplefinResults{}
	if noTransactions {
		var raw struct {
			Accounts []json.RawMessage `json:"accounts"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
		results.RawAccounts = raw.Accounts
		return results, nil
	}
	if err := json.Unmarshal(body, results); err != nil {
		return nil, err
	}
	results.Errors = map[string][]simplefinProviderError{}
	results.HasError = false

	return results, nil
}
