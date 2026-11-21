package store

import (
	"errors"
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/google/uuid"
)

// Nullable tells "not provided" apart from "set to null". It is the Go
// equivalent of a JS property being undefined versus null.
type Nullable[T any] struct {
	Set bool
	Val *T
}

// Value marks a field as set to a real value.
func Value[T any](value T) Nullable[T] {
	return Nullable[T]{Set: true, Val: &value}
}

// Null marks a field as set to null.
func Null[T any]() Nullable[T] {
	return Nullable[T]{Set: true}
}

// FileUpdate only touches the fields that are set.
type FileUpdate struct {
	Name         Nullable[string]
	GroupID      Nullable[string]
	SyncVersion  Nullable[float64]
	EncryptMeta  Nullable[string]
	EncryptSalt  Nullable[string]
	EncryptTest  Nullable[string]
	EncryptKeyID Nullable[string]
	Deleted      Nullable[bool]
	Owner        Nullable[string]
}

// NewGroupID returns a UUID, the same shape the Node server generates.
func NewGroupID() string {
	return uuid.NewString()
}

// LiveFile finds a budget file that has not been deleted. A deleted file is
// reported as missing, matching the Node server's FilesService.get().
func (s *Store) LiveFile(id string) (*File, error) {
	record, err := s.app.FindRecordById("actual_files", id)
	if err != nil {
		return nil, ErrNotFound
	}
	if record.GetBool("deleted") {
		return nil, ErrNotFound
	}

	return fileFromRecord(record), nil
}

// FileExists reports whether a budget record exists, deleted or not.
func (s *Store) FileExists(id string) bool {
	_, err := s.app.FindRecordById("actual_files", id)
	return err == nil
}

// FileRecord returns the raw record, for callers that need the blob field.
func (s *Store) FileRecord(id string) (*core.Record, error) {
	record, err := s.app.FindRecordById("actual_files", id)
	if err != nil {
		return nil, ErrNotFound
	}
	return record, nil
}

// InsertFile creates a budget. The primary key prevents reusing even a
// soft-deleted record's id, including during concurrent uploads.
func (s *Store) InsertFile(id string, update FileUpdate, blob *filesystem.File) error {
	collection, err := s.app.FindCachedCollectionByNameOrId("actual_files")
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("id", id)
	return s.saveFile(record, update, blob)
}

// PutFile updates an existing budget, applying only the fields the update
// defines. When blob is non-nil it replaces the stored budget in the same save.
func (s *Store) PutFile(id string, update FileUpdate, blob *filesystem.File) (*File, error) {
	record, err := s.app.FindRecordById("actual_files", id)
	if err != nil {
		return nil, err
	}
	if err := s.saveFile(record, update, blob); err != nil {
		return nil, err
	}
	return fileFromRecord(record), nil
}

func (s *Store) saveFile(record *core.Record, update FileUpdate, blob *filesystem.File) error {
	applyFileUpdate(record, update)
	if blob != nil {
		record.Set("blob", blob)
	}
	return s.app.Save(record)
}

// ListFiles returns the live budgets a user can see: everything for an admin,
// otherwise the budgets they own plus the ones shared with them.
func (s *Store) ListFiles(userID string, limit int) ([]*File, error) {
	if s.IsAdmin(userID) {
		return s.filterFiles("deleted = false", limit)
	}

	owned, err := s.filterFiles("deleted = false && owner = {:user}", limit, map[string]any{"user": userID})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(owned))
	files := make([]*File, 0, len(owned))
	for _, file := range owned {
		seen[file.ID] = true
		files = append(files, file)
	}

	grants, err := s.app.FindRecordsByFilter(
		"actual_user_access",
		"user_id = {:user}",
		"",
		0,
		0,
		map[string]any{"user": userID},
	)
	if err != nil {
		return nil, fmt.Errorf("read user access: %w", err)
	}

	for _, grant := range grants {
		if len(files) >= limit {
			break
		}
		fileID := grant.GetString("file_id")
		if seen[fileID] {
			continue
		}
		file, err := s.LiveFile(fileID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		seen[fileID] = true
		files = append(files, file)
	}

	return files, nil
}

// FileUser is one entry of a budget's user list.
type FileUser struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	UserName    string `json:"userName"`
}

// FileUsers lists the users with access to a file, grants first and the owner
// last, matching the Node server's UNION ALL.
func (s *Store) FileUsers(fileID string) ([]FileUser, error) {
	file, err := s.app.FindRecordById("actual_files", fileID)
	if err != nil {
		return nil, ErrNotFound
	}

	grants, err := s.app.FindRecordsByFilter(
		"actual_user_access",
		"file_id = {:file}",
		"",
		0,
		0,
		map[string]any{"file": fileID},
	)
	if err != nil {
		return nil, fmt.Errorf("read user access: %w", err)
	}

	users := make([]FileUser, 0, len(grants)+1)
	for _, grant := range grants {
		user, err := s.app.FindRecordById("users", grant.GetString("user_id"))
		if err != nil {
			continue
		}
		users = append(users, fileUserFromRecord(user))
	}

	if ownerID := file.GetString("owner"); ownerID != "" {
		if owner, err := s.app.FindRecordById("users", ownerID); err == nil {
			users = append(users, fileUserFromRecord(owner))
		}
	}

	return users, nil
}

// SetFileOwner reassigns a budget.
func (s *Store) SetFileOwner(fileID, userID string) error {
	record, err := s.app.FindRecordById("actual_files", fileID)
	if err != nil {
		return ErrNotFound
	}

	record.Set("owner", userID)

	return s.app.Save(record)
}

func (s *Store) filterFiles(filter string, limit int, params ...map[string]any) ([]*File, error) {
	var queryParams map[string]any
	if len(params) > 0 {
		queryParams = params[0]
	}

	records, err := s.app.FindRecordsByFilter("actual_files", filter, "", limit, 0, queryParams)
	if err != nil {
		return nil, fmt.Errorf("read files: %w", err)
	}

	files := make([]*File, 0, len(records))
	for _, record := range records {
		files = append(files, fileFromRecord(record))
	}

	return files, nil
}

func applyFileUpdate(record *core.Record, update FileUpdate) {
	apply := func(field string, value any) {
		record.Set(field, value)
	}

	if update.Name.Set {
		apply("name", nullableValue(update.Name))
	}
	if update.GroupID.Set {
		apply("group_id", nullableValue(update.GroupID))
	}
	if update.SyncVersion.Set {
		apply("sync_version", nullableValue(update.SyncVersion))
	}
	if update.EncryptMeta.Set {
		apply("encrypt_meta", nullableValue(update.EncryptMeta))
	}
	if update.EncryptSalt.Set {
		apply("encrypt_salt", nullableValue(update.EncryptSalt))
	}
	if update.EncryptTest.Set {
		apply("encrypt_test", nullableValue(update.EncryptTest))
	}
	if update.EncryptKeyID.Set {
		apply("encrypt_keyid", nullableValue(update.EncryptKeyID))
	}
	if update.Deleted.Set {
		apply("deleted", nullableValue(update.Deleted))
	}
	if update.Owner.Set {
		apply("owner", nullableValue(update.Owner))
	}
}

func nullableValue[T any](value Nullable[T]) any {
	if value.Val == nil {
		return nil
	}
	return *value.Val
}

func fileUserFromRecord(record *core.Record) FileUser {
	return FileUser{
		UserID:      record.Id,
		DisplayName: record.GetString("name"),
		UserName:    record.GetString("email"),
	}
}
