// Package store holds the typed accessors over PocketBase's `users` collection
// and the `actual_*` collections, so routes never deal with raw records.
package store

import (
	"errors"
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// ErrNotFound is returned when a lookup finds no record.
var ErrNotFound = errors.New("not found")

// Store reads and writes the Actual collections.
type Store struct {
	app core.App
}

// New creates a store bound to app.
func New(app core.App) *Store {
	return &Store{app: app}
}

// App exposes the underlying PocketBase app.
func (s *Store) App() core.App { return s.app }

// File is a row of actual_files with the nullable columns made explicit, so
// the sync validation can mirror the Node server's `null` checks.
type File struct {
	ID           string
	GroupID      *string
	SyncVersion  *float64
	EncryptMeta  *string
	EncryptKeyID *string
	EncryptSalt  *string
	EncryptTest  *string
	Deleted      bool
	Name         *string
	Owner        string
}

// File finds a budget file by id.
func (s *Store) File(id string) (*File, error) {
	record, err := s.app.FindRecordById("actual_files", id)
	if err != nil {
		return nil, ErrNotFound
	}

	return fileFromRecord(record), nil
}

// IsAdmin reports whether the user has the ADMIN role.
func (s *Store) IsAdmin(userID string) bool {
	record, err := s.app.FindRecordById("users", userID)
	if err != nil {
		return false
	}
	return record.GetString("role") == "ADMIN"
}

// CountUserAccess counts the grants that let userID access fileID, including
// ownership (Node's countUserAccess).
func (s *Store) CountUserAccess(fileID, userID string) (int, error) {
	file, err := s.app.FindRecordById("actual_files", fileID)
	if err != nil {
		return 0, nil
	}
	if file.GetString("owner") == userID {
		return 1, nil
	}

	access, err := s.app.FindFirstRecordByFilter(
		"actual_user_access",
		"user_id = {:user} && file_id = {:file}",
		map[string]any{"user": userID, "file": fileID},
	)
	if err != nil || access == nil {
		return 0, nil
	}

	return 1, nil
}

// CanAccessActiveFile mirrors user-service.ts: the file must exist, not be
// deleted, and the user must own it, be an admin, or have an explicit grant.
func (s *Store) CanAccessActiveFile(fileID, userID string) (bool, error) {
	file, err := s.File(fileID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if file.Deleted {
		return false, nil
	}
	if s.IsAdmin(userID) {
		return true, nil
	}

	count, err := s.CountUserAccess(fileID, userID)
	if err != nil {
		return false, fmt.Errorf("count user access: %w", err)
	}

	return count > 0, nil
}

// fileFromRecord maps a record onto File.
func fileFromRecord(record *core.Record) *File {
	file := &File{
		ID:      record.Id,
		Deleted: record.GetBool("deleted"),
		Owner:   record.GetString("owner"),
	}

	if value, ok := record.Get("group_id").(string); ok && value != "" {
		file.GroupID = &value
	}
	if value, ok := record.Get("sync_version").(float64); ok {
		file.SyncVersion = &value
	}
	if value, ok := record.Get("encrypt_meta").(string); ok && value != "" {
		file.EncryptMeta = &value
	}
	if value, ok := record.Get("encrypt_keyid").(string); ok && value != "" {
		file.EncryptKeyID = &value
	}
	if value, ok := record.Get("encrypt_salt").(string); ok && value != "" {
		file.EncryptSalt = &value
	}
	if value, ok := record.Get("encrypt_test").(string); ok && value != "" {
		file.EncryptTest = &value
	}
	if value, ok := record.Get("name").(string); ok && value != "" {
		file.Name = &value
	}

	return file
}
