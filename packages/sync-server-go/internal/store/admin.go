package store

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// FieldPasswordSet marks users who chose a password. OAuth2-only users get a
// random password nobody knows, so the flag stays false.
const FieldPasswordSet = "password_set"

// SetPassword sets a real password and marks the user as having one.
func SetPassword(record *core.Record, password string) {
	record.SetPassword(password)
	record.Set(FieldPasswordSet, true)
}

// SetRandomPassword sets an unusable random password (an OAuth2-only user) and
// clears the flag.
func SetRandomPassword(record *core.Record) {
	record.SetRandomPassword()
	record.Set(FieldPasswordSet, false)
}

// AdminUser is the shape the user directory expects.
type AdminUser struct {
	ID          string `json:"id"`
	UserName    string `json:"userName"`
	DisplayName string `json:"displayName"`
	Enabled     bool   `json:"enabled"`
	Owner       bool   `json:"owner"`
	Role        string `json:"role"`
}

// AdminUsers lists every user.
func (s *Store) AdminUsers() ([]AdminUser, error) {
	records, err := s.app.FindRecordsByFilter("users", "", "created", 0, 0)
	if err != nil {
		return nil, fmt.Errorf("read users: %w", err)
	}

	users := make([]AdminUser, 0, len(records))
	for _, record := range records {
		users = append(users, AdminUser{
			ID:          record.Id,
			UserName:    record.GetString("email"),
			DisplayName: record.GetString("name"),
			Enabled:     record.GetBool("enabled"),
			Owner:       record.GetBool("owner"),
			Role:        record.GetString("role"),
		})
	}

	return users, nil
}

// CreateUserParams describes a new user.
type CreateUserParams struct {
	Email       string
	DisplayName string
	Role        string
	Enabled     bool
	Owner       bool
	// Password is optional: without it the user gets a random, unknown one
	// (an OAuth2-only account).
	Password *string
}

// CreateUser inserts a user with PB auth fields set up.
func (s *Store) CreateUser(params CreateUserParams) (*core.Record, error) {
	collection, err := s.app.FindCollectionByNameOrId("users")
	if err != nil {
		return nil, fmt.Errorf("find users: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("email", params.Email)
	record.Set("name", params.DisplayName)
	record.Set("role", params.Role)
	record.Set("enabled", params.Enabled)
	record.Set("owner", params.Owner)
	record.Set("verified", true)

	if params.Password != nil {
		SetPassword(record, *params.Password)
	} else {
		SetRandomPassword(record)
	}

	if err := s.app.Save(record); err != nil {
		return nil, err
	}

	return record, nil
}

// UpdateUserParams describes an edit. Password is only changed when set.
type UpdateUserParams struct {
	ID          string
	Email       string
	DisplayName string
	Role        string
	Enabled     bool
	Password    *string
}

// UpdateUser edits a user.
func (s *Store) UpdateUser(params UpdateUserParams) error {
	record, err := s.app.FindRecordById("users", params.ID)
	if err != nil {
		return ErrNotFound
	}

	record.Set("email", params.Email)
	record.Set("name", params.DisplayName)
	record.Set("role", params.Role)
	record.Set("enabled", params.Enabled)

	if params.Password != nil {
		SetPassword(record, *params.Password)
	}

	return s.app.Save(record)
}

// Owner returns the server owner record, if one exists.
func (s *Store) Owner() (*core.Record, error) {
	record, err := s.app.FindFirstRecordByFilter("users", "owner = true")
	if err != nil {
		return nil, ErrNotFound
	}
	return record, nil
}

// OwnerID is the owner's id, or "" when no owner exists yet.
func (s *Store) OwnerID() string {
	owner, err := s.Owner()
	if err != nil {
		return ""
	}
	return owner.Id
}

// UserExists reports whether a users record exists.
func (s *Store) UserExists(id string) bool {
	if id == "" {
		return false
	}
	_, err := s.app.FindRecordById("users", id)
	return err == nil
}

// EmailTakenByAnotherUser reports whether email belongs to a user other than
// exceptID.
func (s *Store) EmailTakenByAnotherUser(email, exceptID string) bool {
	record, err := s.app.FindAuthRecordByEmail("users", email)
	if err != nil {
		return false
	}
	return record.Id != exceptID
}

// AccessUser is a user with access to one budget.
type AccessUser struct {
	UserID      string `json:"userId"`
	UserName    string `json:"userName"`
	DisplayName string `json:"displayName"`
	Owner       string `json:"owner"`
}

// FileAccessUsers lists the explicit grants of a budget.
func (s *Store) FileAccessUsers(fileID string) ([]AccessUser, error) {
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

	users := make([]AccessUser, 0, len(grants))
	for _, grant := range grants {
		user, err := s.app.FindRecordById("users", grant.GetString("user_id"))
		if err != nil {
			continue
		}
		users = append(users, AccessUser{
			UserID:      user.Id,
			UserName:    user.GetString("email"),
			DisplayName: user.GetString("name"),
			Owner:       file.GetString("owner"),
		})
	}

	return users, nil
}

// UserAccessState is one row of the "who could I share this with" list.
type UserAccessState struct {
	UserID      string `json:"userId"`
	UserName    string `json:"userName"`
	DisplayName string `json:"displayName"`
	HaveAccess  int    `json:"haveAccess"`
	Owner       int    `json:"owner"`
}

// AllUserAccess lists every enabled user with their access flags for a budget.
func (s *Store) AllUserAccess(fileID string) ([]UserAccessState, error) {
	users, err := s.app.FindRecordsByFilter("users", "enabled = true", "created", 0, 0)
	if err != nil {
		return nil, fmt.Errorf("read users: %w", err)
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
	granted := make(map[string]bool, len(grants))
	for _, grant := range grants {
		granted[grant.GetString("user_id")] = true
	}

	ownerID := ""
	if file, err := s.app.FindRecordById("actual_files", fileID); err == nil {
		ownerID = file.GetString("owner")
	}

	states := make([]UserAccessState, 0, len(users))
	for _, user := range users {
		haveAccess := 0
		if granted[user.Id] || user.Id == ownerID {
			haveAccess = 1
		}
		isOwner := 0
		if user.Id == ownerID {
			isOwner = 1
		}
		states = append(states, UserAccessState{
			UserID:      user.Id,
			UserName:    user.GetString("email"),
			DisplayName: user.GetString("name"),
			HaveAccess:  haveAccess,
			Owner:       isOwner,
		})
	}

	return states, nil
}

// AddUserAccess grants a user access to a budget.
func (s *Store) AddUserAccess(userID, fileID string) error {
	collection, err := s.app.FindCollectionByNameOrId("actual_user_access")
	if err != nil {
		return fmt.Errorf("find actual_user_access: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("user_id", userID)
	record.Set("file_id", fileID)

	return s.app.Save(record)
}

// DeleteUserAccess removes grants for the given users on one budget and returns
// how many rows were deleted.
func (s *Store) DeleteUserAccess(userIDs []string, fileID string) (int, error) {
	deleted := 0

	err := s.app.RunInTransaction(func(txApp core.App) error {
		for _, userID := range userIDs {
			record, err := txApp.FindFirstRecordByFilter(
				"actual_user_access",
				"user_id = {:user} && file_id = {:file}",
				map[string]any{"user": userID, "file": fileID},
			)
			if err != nil {
				continue
			}
			if err := txApp.Delete(record); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})

	return deleted, err
}

// DeleteAllUserAccess removes every grant held by a user.
func (s *Store) DeleteAllUserAccess(userID string) error {
	records, err := s.app.FindRecordsByFilter(
		"actual_user_access",
		"user_id = {:user}",
		"",
		0,
		0,
		map[string]any{"user": userID},
	)
	if err != nil {
		return fmt.Errorf("read user access: %w", err)
	}

	for _, record := range records {
		if err := s.app.Delete(record); err != nil {
			return err
		}
	}

	return nil
}

// TransferFiles moves every budget owned by from to to.
func (s *Store) TransferFiles(fromUserID, toUserID string) error {
	records, err := s.app.FindRecordsByFilter(
		"actual_files",
		"owner = {:owner}",
		"",
		0,
		0,
		map[string]any{"owner": fromUserID},
	)
	if err != nil {
		return fmt.Errorf("read files: %w", err)
	}

	for _, record := range records {
		record.Set("owner", toUserID)
		if err := s.app.Save(record); err != nil {
			return err
		}
	}

	return nil
}

// APIKey is one of a user's budget-scoped keys.
type APIKey struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	CreatedAt int64    `json:"createdAt"`
	FileIDs   []string `json:"fileIds"`
}

// CreateAPIKey stores a new key and its budget grants. It returns the metadata
// and the one-time secret, which is never stored in clear text.
func (s *Store) CreateAPIKey(userID, name string, fileIDs []string) (*APIKey, string, error) {
	secret, err := newAPIKeySecret()
	if err != nil {
		return nil, "", err
	}

	createdAt := time.Now().Unix()
	key := &APIKey{
		Name:      name,
		CreatedAt: createdAt,
		FileIDs:   append([]string{}, fileIDs...),
	}

	err = s.app.RunInTransaction(func(txApp core.App) error {
		collection, err := txApp.FindCollectionByNameOrId("actual_api_keys")
		if err != nil {
			return fmt.Errorf("find actual_api_keys: %w", err)
		}

		record := core.NewRecord(collection)
		record.Set("user_id", userID)
		record.Set("name", name)
		record.Set("key_hash", HashToken(secret))
		record.Set("created_at", createdAt)
		if err := txApp.Save(record); err != nil {
			return err
		}
		key.ID = record.Id

		budgets, err := txApp.FindCollectionByNameOrId("actual_api_key_budgets")
		if err != nil {
			return fmt.Errorf("find actual_api_key_budgets: %w", err)
		}

		for _, fileID := range fileIDs {
			grant := core.NewRecord(budgets)
			grant.Set("key_id", key.ID)
			grant.Set("file_id", fileID)
			if err := txApp.Save(grant); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, "", err
	}

	return key, secret, nil
}

// APIKeys lists a user's keys, newest first.
func (s *Store) APIKeys(userID string) ([]APIKey, error) {
	records, err := s.app.FindRecordsByFilter(
		"actual_api_keys",
		"user_id = {:user}",
		"",
		0,
		0,
		map[string]any{"user": userID},
	)
	if err != nil {
		return nil, fmt.Errorf("read api keys: %w", err)
	}

	sort.SliceStable(records, func(i, j int) bool {
		left, right := records[i], records[j]
		if left.GetInt("created_at") != right.GetInt("created_at") {
			return left.GetInt("created_at") > right.GetInt("created_at")
		}
		return left.Id < right.Id
	})

	keys := make([]APIKey, 0, len(records))
	for _, record := range records {
		fileIDs, err := s.APIKeyFileIDs(record.Id)
		if err != nil {
			return nil, err
		}
		keys = append(keys, APIKey{
			ID:        record.Id,
			Name:      record.GetString("name"),
			CreatedAt: int64(record.GetInt("created_at")),
			FileIDs:   fileIDs,
		})
	}

	return keys, nil
}

// DeleteAPIKey removes a key owned by userID.
func (s *Store) DeleteAPIKey(userID, id string) (bool, error) {
	record, err := s.app.FindRecordById("actual_api_keys", id)
	if err != nil {
		return false, nil
	}
	if record.GetString("user_id") != userID {
		return false, nil
	}

	return true, s.app.Delete(record)
}

// newAPIKeySecret generates `actual_` plus 32 random bytes in base64url,
// matching app-api-keys.ts.
func newAPIKeySecret() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", errors.Join(errors.New("generate api key"), err)
	}

	return "actual_" + base64.RawURLEncoding.EncodeToString(buffer), nil
}
