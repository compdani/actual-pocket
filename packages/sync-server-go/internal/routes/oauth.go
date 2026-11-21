// OAuth2 login. Providers are configured in the PocketBase admin UI on the
// users collection; this file only drives the browser flow and maps the
// provider's identity onto a users record.
package routes

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/auth"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"golang.org/x/oauth2"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

// pendingOAuthLifetime is how long a started login may take to come back.
const pendingOAuthLifetime = 10 * time.Minute

// Err reasons for the OAuth2 callback. Node's app-openid.ts returns 400 JSON
// with these reasons, not a redirect, so the callback page never sees them.
var (
	errOAuthUserNotFound = errors.New("user not found")
	errOAuthUserDisabled = errors.New("user disabled")
)

func registerOAuth2(actual *router.RouterGroup[*core.RequestEvent], opts Options) {
	actual.GET("/openid/callback", opts.oauthCallback)
}

// startOAuth begins an OAuth2 login: it validates the return URL, builds the
// provider's authorization URL with PKCE, and stores the single-use state.
func (o Options) startOAuth(e *core.RequestEvent, body loginRequest) error {
	users, err := o.App.FindCachedCollectionByNameOrId("users")
	if err != nil {
		return httpx.Error(e)
	}

	appURL := strings.TrimRight(o.App.Settings().Meta.AppURL, "/")
	if appURL == "" {
		o.App.Logger().Error("OAuth2 login needs the Application URL setting in the PocketBase admin UI")
		return httpx.Fail(e, http.StatusBadRequest, "app-url-not-set")
	}

	if !isValidRedirectURL(body.ReturnURL, appURL) {
		return httpx.Fail(e, http.StatusBadRequest, "Invalid redirect URL")
	}

	provider, err := o.provider(users, body.Provider)
	if err != nil {
		return httpx.Fail(e, http.StatusBadRequest, "provider-not-configured")
	}
	provider.SetRedirectURL(appURL + "/openid/callback")

	verifier := oauth2.GenerateVerifier()
	state := security.RandomString(32)

	pendingCollection, err := o.App.FindCachedCollectionByNameOrId("actual_pending_oauth")
	if err != nil {
		return httpx.Error(e)
	}

	pending := core.NewRecord(pendingCollection)
	pending.Set("state", state)
	pending.Set("provider", body.Provider)
	pending.Set("code_verifier", verifier)
	pending.Set("return_url", body.ReturnURL)
	pending.Set("expires_at", time.Now().Add(pendingOAuthLifetime).Unix())

	if err := o.App.Save(pending); err != nil {
		return httpx.Error(e)
	}

	authURL := provider.BuildAuthURL(state, oauth2.S256ChallengeOption(verifier))

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: struct {
		ReturnURL string `json:"returnUrl"`
	}{ReturnURL: authURL}})
}

// oauthCallback completes an OAuth2 login started by startOAuth.
func (o Options) oauthCallback(e *core.RequestEvent) error {
	query := e.Request.URL.Query()
	code := query.Get("code")
	state := query.Get("state")

	if code == "" {
		return httpx.Fail(e, http.StatusBadRequest, "missing-authorization-code")
	}
	if state == "" {
		return httpx.Fail(e, http.StatusBadRequest, "missing-state")
	}

	users, err := o.App.FindCachedCollectionByNameOrId("users")
	if err != nil {
		return httpx.Error(e)
	}
	if !users.OAuth2.Enabled {
		return httpx.Fail(e, http.StatusBadRequest, "openid-not-configured")
	}

	pending, err := o.App.FindFirstRecordByFilter(
		"actual_pending_oauth",
		"state = {:state}",
		dbx.Params{"state": state},
	)
	if err != nil {
		return httpx.Fail(e, http.StatusBadRequest, "invalid-or-expired-state")
	}

	// Single use: consume the row before any network call, so a replay can
	// never exchange the same code twice.
	expired := int64(pending.GetInt("expires_at")) <= time.Now().Unix()
	providerName := pending.GetString("provider")
	verifier := pending.GetString("code_verifier")
	returnURL := pending.GetString("return_url")

	if err := o.App.Delete(pending); err != nil {
		return httpx.Error(e)
	}
	if expired {
		return httpx.Fail(e, http.StatusBadRequest, "invalid-or-expired-state")
	}

	provider, err := o.provider(users, providerName)
	if err != nil {
		return httpx.Fail(e, http.StatusBadRequest, "openid-not-configured")
	}
	appURL := strings.TrimRight(o.App.Settings().Meta.AppURL, "/")
	if appURL == "" {
		o.App.Logger().Error("OAuth2 callback needs the Application URL setting in the PocketBase admin UI")
		return httpx.Fail(e, http.StatusBadRequest, "app-url-not-set")
	}
	provider.SetRedirectURL(appURL + "/openid/callback")

	tokenSet, err := provider.FetchToken(code, oauth2.VerifierOption(verifier))
	if err != nil {
		o.App.Logger().Error("OAuth2 token exchange failed", "provider", providerName, "error", err)
		return httpx.Fail(e, http.StatusBadRequest, "openid-grant-failed")
	}

	oauthUser, err := provider.FetchAuthUser(tokenSet)
	if err != nil {
		o.App.Logger().Error("OAuth2 userinfo failed", "provider", providerName, "error", err)
		return httpx.Fail(e, http.StatusBadRequest, "openid-grant-failed")
	}

	user, err := o.resolveOAuthUser(users, providerName, oauthUser)
	switch {
	case errors.Is(err, errOAuthUserNotFound):
		return httpx.Fail(e, http.StatusBadRequest, "user-not-found")
	case errors.Is(err, errOAuthUserDisabled):
		// Node reports a disabled OAuth2 user as a failed grant.
		return httpx.Fail(e, http.StatusBadRequest, "openid-grant-failed")
	case err != nil:
		o.App.Logger().Error("OAuth2 user resolution failed", "provider", providerName, "error", err)
		return httpx.Error(e)
	}

	token, err := o.Auth.IssueToken(user)
	if err != nil {
		return httpx.Error(e)
	}

	return e.Redirect(http.StatusFound,
		strings.TrimRight(returnURL, "/")+"/openid-cb?token="+url.QueryEscape(token))
}

// provider resolves a configured OAuth2 provider on the users collection.
func (o Options) provider(users *core.Collection, name string) (auth.Provider, error) {
	if !users.OAuth2.Enabled {
		return nil, errors.New("oauth2 disabled")
	}

	config, ok := users.OAuth2.GetProviderConfig(name)
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", name)
	}

	provider, err := config.InitProvider()
	if err != nil {
		return nil, fmt.Errorf("init provider %q: %w", name, err)
	}

	return provider, nil
}

// resolveOAuthUser maps a provider identity onto a users record, in the order
// from the plan: an existing external-auth link, then an email match, then
// auto-creation when allowed.
func (o Options) resolveOAuthUser(users *core.Collection, provider string, oauthUser *auth.AuthUser) (*core.Record, error) {
	if linked, err := o.userByExternalAuth(users, provider, oauthUser.Id); err == nil {
		if !linked.GetBool("enabled") {
			return nil, errOAuthUserDisabled
		}
		return linked, nil
	}

	if oauthUser.Email != "" {
		user, err := o.App.FindAuthRecordByEmail("users", oauthUser.Email)
		if err == nil {
			if !user.GetBool("enabled") {
				return nil, errOAuthUserDisabled
			}
			if err := o.linkExternalAuth(provider, oauthUser, user); err != nil {
				return nil, err
			}
			return user, nil
		}
	}

	count, err := o.App.CountRecords("users")
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}

	if count > 0 && o.Config.UserCreationMode != "login" {
		return nil, errOAuthUserNotFound
	}
	if oauthUser.Email == "" {
		// A users record needs an email; without one there is nothing to create.
		return nil, errOAuthUserNotFound
	}

	return o.createOAuthUser(provider, oauthUser)
}

// userByExternalAuth finds the user linked to a provider identity.
func (o Options) userByExternalAuth(users *core.Collection, provider, providerID string) (*core.Record, error) {
	link, err := o.App.FindFirstExternalAuthByExpr(dbx.HashExp{
		"collectionRef": users.Id,
		"provider":      provider,
		"providerId":    providerID,
	})
	if err != nil {
		return nil, err
	}

	user, err := o.App.FindRecordById("users", link.RecordRef())
	if err != nil {
		return nil, err
	}

	return user, nil
}

// linkExternalAuth records the provider identity for an existing user.
func (o Options) linkExternalAuth(provider string, oauthUser *auth.AuthUser, user *core.Record) error {
	// The link may already exist for this user (for example if an earlier
	// callback created it after the lookup).
	if linked, err := o.userByExternalAuth(user.Collection(), provider, oauthUser.Id); err == nil && linked.Id == user.Id {
		return nil
	}

	return o.saveExternalAuth(o.App, provider, oauthUser, user)
}

// saveExternalAuth creates the _externalAuths link.
func (o Options) saveExternalAuth(app core.App, provider string, oauthUser *auth.AuthUser, user *core.Record) error {
	link := core.NewExternalAuth(app)
	link.SetCollectionRef(user.Collection().Id)
	link.SetRecordRef(user.Id)
	link.SetProvider(provider)
	link.SetProviderId(oauthUser.Id)

	if err := app.Save(link); err != nil {
		return fmt.Errorf("save external auth link: %w", err)
	}

	return nil
}

// createOAuthUser provisions a user for a first login, bypassing the users
// create rule: rules only guard PocketBase's REST API, and an OAuth2 login is a
// trusted server-side decision. The first user becomes the ADMIN owner.
func (o Options) createOAuthUser(provider string, oauthUser *auth.AuthUser) (*core.Record, error) {
	var created *core.Record

	err := o.App.RunInTransaction(func(txApp core.App) error {
		// Re-check inside the transaction so two simultaneous first logins
		// cannot both create the same user.
		count, err := txApp.CountRecords("users")
		if err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		if count > 0 && o.Config.UserCreationMode != "login" {
			return errOAuthUserNotFound
		}

		if existing, err := txApp.FindAuthRecordByEmail("users", oauthUser.Email); err == nil {
			if !existing.GetBool("enabled") {
				return errOAuthUserDisabled
			}
			if err := o.saveExternalAuth(txApp, provider, oauthUser, existing); err != nil {
				return err
			}
			created = existing
			return nil
		}

		collection, err := txApp.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		record := core.NewRecord(collection)
		record.Set("email", oauthUser.Email)
		record.Set("name", oauthUser.Name)
		record.Set("role", "BASIC")
		record.Set("enabled", true)
		record.Set("owner", false)
		record.Set("verified", true)

		if count == 0 {
			record.Set("role", "ADMIN")
			record.Set("owner", true)
		}

		// OAuth2-only users have no password they know.
		store.SetRandomPassword(record)

		if err := txApp.Save(record); err != nil {
			return err
		}

		if err := o.saveExternalAuth(txApp, provider, oauthUser, record); err != nil {
			return err
		}

		created = record

		return nil
	})

	if err != nil {
		return nil, err
	}

	return created, nil
}

// isValidRedirectURL is Node's isValidRedirectUrl from accounts/openid.ts: the
// return URL must point at the same hostname as the configured application, or
// at localhost. Ports and schemes are deliberately ignored, exactly like Node.
func isValidRedirectURL(raw, appURL string) bool {
	if raw == "" || appURL == "" {
		return false
	}

	redirectURL, err := url.Parse(raw)
	if err != nil || redirectURL.Host == "" {
		return false
	}

	serverURL, err := url.Parse(appURL)
	if err != nil || serverURL.Host == "" {
		return false
	}

	return redirectURL.Hostname() == serverURL.Hostname() || redirectURL.Hostname() == "localhost"
}
