package store

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// SecretNames is the allow list from packages/sync-server/src/services/secrets-service.js.
// A secret is stored under `<name>` (global) or `<name>:<fileId>` (per budget).
var SecretNames = []string{
	"gocardless_secretId",
	"gocardless_secretKey",
	"simplefin_token",
	"simplefin_accessKey",
	"pluggyai_clientId",
	"pluggyai_clientSecret",
	"pluggyai_itemIds",
	"akahu_userToken",
	"akahu_appToken",
	"enablebanking_applicationId",
	"enablebanking_secretKey",
}

// IsSecretName reports whether name is an allowed secret key.
func IsSecretName(name string) bool {
	for _, known := range SecretNames {
		if known == name {
			return true
		}
	}
	return false
}

// SecretKey is the stored name for a secret, global when fileID is empty.
func SecretKey(name, fileID string) string {
	if fileID == "" {
		return name
	}
	return name + ":" + fileID
}

// Secret reads a secret value. A missing secret returns "" plus false.
func (s *Store) Secret(name, fileID string) (string, bool, error) {
	record, err := s.app.FindFirstRecordByFilter(
		"actual_secrets",
		"name = {:name}",
		map[string]any{"name": SecretKey(name, fileID)},
	)
	if err != nil {
		if isNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read secret: %w", err)
	}

	return record.GetString("value"), true, nil
}

// SetSecret upserts a secret.
func (s *Store) SetSecret(name, fileID, value string) error {
	return s.app.RunInTransaction(func(txApp core.App) error {
		return setSecret(txApp, name, fileID, value)
	})
}

func setSecret(app core.App, name, fileID, value string) error {
	collection, err := app.FindCollectionByNameOrId("actual_secrets")
	if err != nil {
		return fmt.Errorf("find actual_secrets: %w", err)
	}

	key := SecretKey(name, fileID)

	record, err := app.FindFirstRecordByFilter(
		"actual_secrets",
		"name = {:name}",
		map[string]any{"name": key},
	)
	if err != nil || record == nil {
		record = core.NewRecord(collection)
		record.Set("name", key)
	}

	record.Set("value", value)

	return app.Save(record)
}

// ResetSecret deletes one secret if it exists.
func (s *Store) ResetSecret(name, fileID string) error {
	return deleteSecret(s.app, name, fileID)
}

func deleteSecret(app core.App, name, fileID string) error {
	record, err := app.FindFirstRecordByFilter(
		"actual_secrets",
		"name = {:name}",
		map[string]any{"name": SecretKey(name, fileID)},
	)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("read secret: %w", err)
	}

	return app.Delete(record)
}
