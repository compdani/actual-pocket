// Migration 1795220900 turns on PocketBase's settings migrations: API rate
// limits (the auth endpoints now accept real user passwords) and nightly
// backups of pb_data, which include the group databases (D13, §4.3).
package migrations

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

// authRateLimit is the equivalent of Node's 5 failed auth requests per 15
// minutes: 5 requests per 900 seconds.
const (
	authRateLimitMaxRequests = 5
	authRateLimitDuration    = 15 * 60
)

// authRateLimitLabels are the collection auth actions that check a password or
// drive an OAuth2 login. `*:auth` (which also covers auth-with-password and
// auth-with-oauth2, but importantly covers `auth-refresh` too) stays at
// PocketBase's default so refreshing a token on app start is not throttled.
var authRateLimitLabels = []string{"*:authWithPassword", "*:authWithOAuth2"}

const (
	backupCron        = "0 3 * * *"
	backupCronMaxKeep = 7
)

func init() {
	pbmigrations.Register(updateSettingsUp, updateSettingsDown)
}

func updateSettingsUp(app core.App) error {
	settings := app.Settings()

	settings.RateLimits.Enabled = true
	for _, label := range authRateLimitLabels {
		settings.RateLimits.Rules = upsertRule(settings.RateLimits.Rules, label, authRateLimitMaxRequests, authRateLimitDuration)
	}

	settings.Backups.Cron = backupCron
	settings.Backups.CronMaxKeep = backupCronMaxKeep
	settings.Backups.S3.Enabled = false

	return app.Save(settings)
}

func updateSettingsDown(app core.App) error {
	settings := app.Settings()

	settings.RateLimits.Enabled = false
	settings.RateLimits.Rules = removeRules(settings.RateLimits.Rules, authRateLimitLabels...)

	settings.Backups.Cron = ""
	settings.Backups.CronMaxKeep = 3
	settings.Backups.S3.Enabled = false

	return app.Save(settings)
}

// upsertRule rewrites the matching rule in place, or appends it, leaving
// PocketBase's other default rules untouched.
func upsertRule(rules []core.RateLimitRule, label string, maxRequests int, duration int64) []core.RateLimitRule {
	out := make([]core.RateLimitRule, 0, len(rules)+1)
	found := false
	for _, rule := range rules {
		if rule.Label == label && rule.Audience == "" {
			rule.MaxRequests = maxRequests
			rule.Duration = duration
			found = true
		}
		out = append(out, rule)
	}
	if !found {
		out = append(out, core.RateLimitRule{
			Label:       label,
			MaxRequests: maxRequests,
			Duration:    duration,
		})
	}
	return out
}

// removeRules drops rules by label, for the down migration.
func removeRules(rules []core.RateLimitRule, labels ...string) []core.RateLimitRule {
	drop := map[string]bool{}
	for _, label := range labels {
		drop[label] = true
	}

	out := make([]core.RateLimitRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Audience == "" && drop[rule.Label] {
			continue
		}
		out = append(out, rule)
	}
	return out
}
