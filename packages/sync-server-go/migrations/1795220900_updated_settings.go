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
	settings.RateLimits.Rules = replaceRule(settings.RateLimits.Rules, "*:auth", authRateLimitMaxRequests, authRateLimitDuration)

	settings.Backups.Cron = backupCron
	settings.Backups.CronMaxKeep = backupCronMaxKeep
	settings.Backups.S3.Enabled = false

	return app.Save(settings)
}

func updateSettingsDown(app core.App) error {
	settings := app.Settings()

	settings.RateLimits.Enabled = false
	settings.RateLimits.Rules = replaceRule(settings.RateLimits.Rules, "*:auth", 2, 3)

	settings.Backups.Cron = ""
	settings.Backups.CronMaxKeep = 3
	settings.Backups.S3.Enabled = false

	return app.Save(settings)
}

// replaceRule rewrites the matching rule in place, keeping PocketBase's other
// defaults untouched.
func replaceRule(rules []core.RateLimitRule, label string, maxRequests int, duration int64) []core.RateLimitRule {
	out := make([]core.RateLimitRule, 0, len(rules))
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
