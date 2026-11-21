package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// HashToken is the SHA-256 hex digest used as the denylist key for a PocketBase
// auth token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CountUsers returns the number of registered users.
func (s *Store) CountUsers() (int, error) {
	count, err := s.app.CountRecords("users")
	if err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return int(count), nil
}

// UserByEmail looks up an auth record by its email (Actual's user_name).
func (s *Store) UserByEmail(email string) (*core.Record, error) {
	record, err := s.app.FindAuthRecordByEmail("users", email)
	if err != nil {
		return nil, ErrNotFound
	}
	return record, nil
}

// UserByID returns a users record.
func (s *Store) UserByID(id string) (*core.Record, error) {
	record, err := s.app.FindRecordById("users", id)
	if err != nil {
		return nil, ErrNotFound
	}
	return record, nil
}

// IsTokenRevoked reports whether the token's hash is in the denylist.
func (s *Store) IsTokenRevoked(tokenHash string) (bool, error) {
	_, err := s.app.FindFirstRecordByFilter(
		"actual_revoked_tokens",
		"token_hash = {:hash}",
		map[string]any{"hash": tokenHash},
	)
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read revoked tokens: %w", err)
	}
	return true, nil
}

// RevokeToken adds a token hash to the denylist. Revoking an already revoked
// token is a no-op.
func (s *Store) RevokeToken(tokenHash string, expiresAt int64) error {
	collection, err := s.app.FindCollectionByNameOrId("actual_revoked_tokens")
	if err != nil {
		return fmt.Errorf("find actual_revoked_tokens: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("token_hash", tokenHash)
	record.Set("expires_at", expiresAt)

	if err := s.app.Save(record); err != nil {
		// The unique index catches a double logout; that is fine.
		if revoked, lookupErr := s.IsTokenRevoked(tokenHash); lookupErr == nil && revoked {
			return nil
		}
		return fmt.Errorf("revoke token: %w", err)
	}

	return nil
}

// PruneRevokedTokens deletes denylist rows whose expiry has passed and returns
// how many were removed.
func (s *Store) PruneRevokedTokens(now int64) (int, error) {
	records, err := s.app.FindRecordsByFilter(
		"actual_revoked_tokens",
		"expires_at < {:now}",
		"",
		0,
		0,
		map[string]any{"now": now},
	)
	if err != nil {
		return 0, fmt.Errorf("read expired tokens: %w", err)
	}

	for _, record := range records {
		if err := s.app.Delete(record); err != nil {
			return 0, fmt.Errorf("delete expired token: %w", err)
		}
	}

	return len(records), nil
}

// PrunePendingOAuth deletes stale OAuth2 login state.
func (s *Store) PrunePendingOAuth(now int64) (int, error) {
	records, err := s.app.FindRecordsByFilter(
		"actual_pending_oauth",
		"expires_at < {:now}",
		"",
		0,
		0,
		map[string]any{"now": now},
	)
	if err != nil {
		return 0, fmt.Errorf("read expired oauth requests: %w", err)
	}

	for _, record := range records {
		if err := s.app.Delete(record); err != nil {
			return 0, fmt.Errorf("delete expired oauth request: %w", err)
		}
	}

	return len(records), nil
}

// APIKeyByHash resolves an API key and its (enabled) user.
func (s *Store) APIKeyByHash(keyHash string) (*core.Record, *core.Record, error) {
	key, err := s.app.FindFirstRecordByFilter(
		"actual_api_keys",
		"key_hash = {:hash}",
		map[string]any{"hash": keyHash},
	)
	if err != nil {
		return nil, nil, ErrNotFound
	}

	user, err := s.app.FindRecordById("users", key.GetString("user_id"))
	if err != nil {
		return nil, nil, ErrNotFound
	}
	if !user.GetBool("enabled") {
		return nil, nil, ErrNotFound
	}

	return key, user, nil
}

// APIKeyFileIDs lists the budgets an API key is scoped to. The result is never
// nil: an empty grant list means "no budgets", not "all budgets".
func (s *Store) APIKeyFileIDs(keyID string) ([]string, error) {
	records, err := s.app.FindRecordsByFilter(
		"actual_api_key_budgets",
		"key_id = {:key}",
		"",
		0,
		0,
		map[string]any{"key": keyID},
	)
	if err != nil {
		return nil, fmt.Errorf("read api key budgets: %w", err)
	}

	fileIDs := make([]string, 0, len(records))
	for _, record := range records {
		fileIDs = append(fileIDs, record.GetString("file_id"))
	}

	return fileIDs, nil
}

// ServerPrefs returns the global server preferences as key/value strings.
func (s *Store) ServerPrefs() (map[string]string, error) {
	records, err := s.app.FindRecordsByFilter("actual_server_prefs", "", "", 0, 0)
	if err != nil {
		return nil, fmt.Errorf("read server prefs: %w", err)
	}

	prefs := make(map[string]string, len(records))
	for _, record := range records {
		prefs[record.GetString("key")] = record.GetString("value")
	}

	return prefs, nil
}

// SetServerPrefs upserts every key of prefs. Non-string values are stored as
// their JSON representation, which matches what the Node server's callers send.
func (s *Store) SetServerPrefs(prefs map[string]any) error {
	return s.app.RunInTransaction(func(txApp core.App) error {
		collection, err := txApp.FindCollectionByNameOrId("actual_server_prefs")
		if err != nil {
			return fmt.Errorf("find actual_server_prefs: %w", err)
		}

		for key, value := range prefs {
			record, err := txApp.FindFirstRecordByFilter(
				"actual_server_prefs",
				"key = {:key}",
				map[string]any{"key": key},
			)
			if err != nil || record == nil {
				record = core.NewRecord(collection)
				record.Set("key", key)
			}

			record.Set("value", prefString(value))
			if err := txApp.Save(record); err != nil {
				return fmt.Errorf("save server pref %q: %w", key, err)
			}
		}

		return nil
	})
}

func prefString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	if string(encoded) == "null" {
		return ""
	}
	if len(encoded) > 1 && encoded[0] == '"' {
		var text string
		if err := json.Unmarshal(encoded, &text); err == nil {
			return text
		}
	}

	return string(encoded)
}

// isNotFound reports whether PB's lookup error means "no rows".
func isNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrNotFound)
}
