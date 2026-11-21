package routes

import (
	"net/http"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

func TestOwnerCreated(t *testing.T) {
	h := newHarness(t)

	// No auth required, like the Node route.
	response := h.getJSON("/admin/owner-created/", "")
	if response.Code != 200 || response.Body.String() != "true" {
		t.Fatalf("owner-created = %d %q, want 200 true", response.Code, response.Body.String())
	}
}

func TestAdminListUsers(t *testing.T) {
	h := newHarness(t)

	// Any authenticated user may list users (Node's accepted trade-off).
	response := h.getJSON("/admin/users/", h.userToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	var users []map[string]any
	if err := decodeInto(t, response, &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 4 {
		t.Fatalf("%d users, want 4", len(users))
	}

	byEmail := map[string]map[string]any{}
	for _, user := range users {
		byEmail[user["userName"].(string)] = user
	}
	admin := byEmail["admin@example.com"]
	if admin == nil {
		t.Fatalf("admin missing from %v", users)
	}
	if admin["owner"] != true || admin["enabled"] != true || admin["role"] != "ADMIN" {
		t.Errorf("admin row = %v", admin)
	}
	if admin["displayName"] != "" || admin["id"] != h.admin.Id {
		t.Errorf("admin row = %v", admin)
	}

	if response := h.getJSON("/admin/users/", ""); response.Code != 401 {
		t.Errorf("unauthenticated status = %d", response.Code)
	}
}

func TestAdminCreateUserWithPassword(t *testing.T) {
	h := newHarness(t)

	response := h.postJSON(t, "/admin/users/", map[string]any{
		"userName":    "new@example.com",
		"displayName": "New User",
		"role":        "BASIC",
		"enabled":     true,
		"password":    "new-user-password",
	}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	body := jsonBody(t, response)
	data := body["data"].(map[string]any)
	id, _ := data["id"].(string)
	if body["status"] != "ok" || id == "" {
		t.Fatalf("body = %v", body)
	}

	record := h.mustUser(t, id)
	if record.GetString("email") != "new@example.com" || record.GetString("name") != "New User" {
		t.Errorf("record = %v", record)
	}
	if record.GetString("role") != "BASIC" || !record.GetBool("enabled") || !record.GetBool("verified") {
		t.Errorf("record flags = role %q enabled %v verified %v",
			record.GetString("role"), record.GetBool("enabled"), record.GetBool("verified"))
	}
	if !record.GetBool("password_set") {
		t.Error("password_set = false, want true")
	}
	if !record.ValidatePassword("new-user-password") {
		t.Error("the password does not work")
	}

	// The new user can log in.
	login := h.postJSON(t, "/account/login", map[string]string{
		"email": "new@example.com", "password": "new-user-password",
	}, "")
	if login.Code != 200 {
		t.Errorf("login status = %d, body = %q", login.Code, login.Body.String())
	}
}

func TestAdminCreateUserWithoutPassword(t *testing.T) {
	h := newHarness(t)

	response := h.postJSON(t, "/admin/users/", map[string]any{
		"userName": "oauth@example.com",
		"role":     "BASIC",
		"enabled":  true,
	}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	id := jsonBody(t, response)["data"].(map[string]any)["id"].(string)
	record := h.mustUser(t, id)
	if record.GetBool("password_set") {
		t.Error("password_set = true, want false for an OAuth2-only user")
	}
	if record.ValidatePassword("") {
		t.Error("an empty password must not authenticate")
	}

	token := h.token(t, record)
	validated := h.getJSON("/account/validate", token)
	if got := jsonBody(t, validated)["data"].(map[string]any)["loginMethod"]; got != "openid" {
		t.Errorf("loginMethod = %v, want openid", got)
	}
}

func TestAdminCreateUserValidation(t *testing.T) {
	h := newHarness(t)

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"missing email", map[string]any{"role": "BASIC"}, "user-cant-be-empty"},
		{"missing role", map[string]any{"userName": "x@example.com"}, "role-cant-be-empty"},
		{"invalid role", map[string]any{"userName": "x@example.com", "role": "SUPER"}, "role-does-not-exists"},
		{"invalid email", map[string]any{"userName": "not-an-email", "role": "BASIC"}, "invalid-email"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := h.postJSON(t, "/admin/users/", testCase.body, h.adminToken)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
			}
			if got := jsonBody(t, response)["reason"]; got != testCase.want {
				t.Errorf("reason = %v, want %q", got, testCase.want)
			}
		})
	}

	duplicate := h.postJSON(t, "/admin/users/", map[string]any{
		"userName": "user@example.com", "role": "BASIC",
	}, h.adminToken)
	if duplicate.Code != 400 {
		t.Fatalf("duplicate status = %d", duplicate.Code)
	}
	if got := jsonBody(t, duplicate)["reason"]; got != "user-already-exists" {
		t.Errorf("duplicate reason = %v", got)
	}

	forbidden := h.postJSON(t, "/admin/users/", map[string]any{
		"userName": "x@example.com", "role": "BASIC",
	}, h.userToken)
	if forbidden.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d", forbidden.Code)
	}
}

func TestAdminUpdateUser(t *testing.T) {
	h := newHarness(t)

	response := h.patchJSON(t, "/admin/users/", map[string]any{
		"id":          h.user.Id,
		"userName":    "renamed@example.com",
		"displayName": "Renamed",
		"role":        "ADMIN",
		"enabled":     true,
		"password":    "renamed-password",
	}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if got := jsonBody(t, response)["data"].(map[string]any)["id"]; got != h.user.Id {
		t.Errorf("id = %v", got)
	}

	record := h.mustUser(t, h.user.Id)
	if record.GetString("email") != "renamed@example.com" || record.GetString("name") != "Renamed" || record.GetString("role") != "ADMIN" {
		t.Errorf("record = %v", record)
	}
	if !record.ValidatePassword("renamed-password") {
		t.Error("the new password does not work")
	}
	if !record.GetBool("password_set") {
		t.Error("password_set = false")
	}
}

func TestAdminUpdateUserPasswordUnchangedWhenOmitted(t *testing.T) {
	h := newHarness(t)

	response := h.patchJSON(t, "/admin/users/", map[string]any{
		"id":          h.user.Id,
		"userName":    "user@example.com",
		"displayName": "Renamed",
		"role":        "BASIC",
		"enabled":     true,
	}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	if !h.mustUser(t, h.user.Id).ValidatePassword("user-password") {
		t.Error("the existing password stopped working")
	}
}

func TestAdminUpdateUserErrors(t *testing.T) {
	h := newHarness(t)

	missing := h.patchJSON(t, "/admin/users/", map[string]any{
		"id": "nope", "userName": "x@example.com", "role": "BASIC",
	}, h.adminToken)
	if missing.Code != 400 || jsonBody(t, missing)["reason"] != "cannot-find-user-to-update" {
		t.Errorf("missing = %d %v", missing.Code, jsonBody(t, missing))
	}

	demote := h.patchJSON(t, "/admin/users/", map[string]any{
		"id": h.admin.Id, "userName": "admin@example.com", "role": "BASIC", "enabled": true,
	}, h.adminToken)
	if demote.Code != http.StatusForbidden || jsonBody(t, demote)["reason"] != "cannot-modify-owner" {
		t.Errorf("demote = %d %v", demote.Code, jsonBody(t, demote))
	}

	disable := h.patchJSON(t, "/admin/users/", map[string]any{
		"id": h.admin.Id, "userName": "admin@example.com", "role": "ADMIN", "enabled": false,
	}, h.adminToken)
	if disable.Code != http.StatusForbidden || jsonBody(t, disable)["reason"] != "cannot-modify-owner" {
		t.Errorf("disable = %d %v", disable.Code, jsonBody(t, disable))
	}

	duplicate := h.patchJSON(t, "/admin/users/", map[string]any{
		"id": h.user.Id, "userName": "other@example.com", "role": "BASIC", "enabled": true,
	}, h.adminToken)
	if duplicate.Code != 400 || jsonBody(t, duplicate)["reason"] != "user-already-exists" {
		t.Errorf("duplicate = %d %v", duplicate.Code, jsonBody(t, duplicate))
	}

	// A basic user may edit their own profile...
	self := h.patchJSON(t, "/admin/users/", map[string]any{
		"id": h.user.Id, "userName": "user@example.com", "displayName": "Self", "role": "BASIC", "enabled": true,
	}, h.userToken)
	if self.Code != 200 {
		t.Errorf("self edit status = %d, body = %q", self.Code, self.Body.String())
	}

	// ...but not another user.
	forbidden := h.patchJSON(t, "/admin/users/", map[string]any{
		"id": h.other.Id, "userName": "other@example.com", "role": "BASIC", "enabled": true,
	}, h.userToken)
	if forbidden.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d", forbidden.Code)
	}
}

func TestAdminDisableUserInvalidatesTokens(t *testing.T) {
	h := newHarness(t)

	response := h.patchJSON(t, "/admin/users/", map[string]any{
		"id": h.user.Id, "userName": "user@example.com", "role": "BASIC", "enabled": false,
	}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("status = %d", response.Code)
	}

	if got := h.getJSON("/account/validate", h.userToken); got.Code != http.StatusUnauthorized {
		t.Errorf("disabled user token status = %d, want 401", got.Code)
	}
}

func TestAdminDeleteUsers(t *testing.T) {
	h := newHarness(t)

	// The user owns a budget and holds an API key.
	h.standardFile(t, "owned", "group-1", "key", h.user.Id)
	keyResponse := h.postJSON(t, "/api-keys", map[string]any{
		"name": "Key", "fileIds": []string{"owned"},
	}, h.userToken)
	if keyResponse.Code != 201 {
		t.Fatalf("api key status = %d, body = %q", keyResponse.Code, keyResponse.Body.String())
	}

	response := h.deleteJSON(t, "/admin/users", map[string]any{"ids": []string{h.user.Id}}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if got := jsonBody(t, response)["data"].(map[string]any)["someDeletionsFailed"]; got != false {
		t.Errorf("someDeletionsFailed = %v", got)
	}

	if _, err := h.app.FindRecordById("users", h.user.Id); err == nil {
		t.Error("the user still exists")
	}
	if got := h.fileRecord(t, "owned").GetString("owner"); got != h.admin.Id {
		t.Errorf("owner = %q, want the server owner", got)
	}
	if keys, err := h.store.APIKeys(h.user.Id); err != nil || len(keys) != 0 {
		t.Errorf("keys = %v, err = %v", keys, err)
	}
}

func TestAdminDeleteUsersPartial(t *testing.T) {
	h := newHarness(t)

	withOwner := h.deleteJSON(t, "/admin/users", map[string]any{"ids": []string{h.admin.Id}}, h.adminToken)
	if withOwner.Code != http.StatusForbidden || jsonBody(t, withOwner)["reason"] != "cannot-modify-owner" {
		t.Errorf("owner delete = %d %v", withOwner.Code, jsonBody(t, withOwner))
	}

	missing := h.deleteJSON(t, "/admin/users", map[string]any{"ids": []string{"missing"}}, h.adminToken)
	if missing.Code != 400 || jsonBody(t, missing)["reason"] != "not-all-deleted" {
		t.Errorf("missing delete = %d %v", missing.Code, jsonBody(t, missing))
	}

	forbidden := h.deleteJSON(t, "/admin/users", map[string]any{"ids": []string{h.other.Id}}, h.userToken)
	if forbidden.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d", forbidden.Code)
	}
}

func TestAdminAccess(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.admin.Id)

	// Empty grants.
	response := h.getJSON("/admin/access?fileId=file-1", h.adminToken)
	if response.Code != 200 || response.Body.String() != "[]" {
		t.Fatalf("access = %d %q", response.Code, response.Body.String())
	}

	// Add a grant.
	add := h.postJSON(t, "/admin/access/", map[string]string{"fileId": "file-1", "userId": h.user.Id}, h.adminToken)
	if add.Code != 200 {
		t.Fatalf("add status = %d, body = %q", add.Code, add.Body.String())
	}

	var access []map[string]any
	response = h.getJSON("/admin/access?fileId=file-1", h.adminToken)
	if err := decodeInto(t, response, &access); err != nil {
		t.Fatal(err)
	}
	if len(access) != 1 {
		t.Fatalf("access = %v", access)
	}
	if access[0]["userId"] != h.user.Id || access[0]["userName"] != "user@example.com" || access[0]["owner"] != h.admin.Id {
		t.Errorf("access row = %v", access[0])
	}

	// Duplicates are rejected.
	duplicate := h.postJSON(t, "/admin/access/", map[string]string{"fileId": "file-1", "userId": h.user.Id}, h.adminToken)
	if duplicate.Code != 400 || jsonBody(t, duplicate)["reason"] != "user-already-have-access" {
		t.Errorf("duplicate = %d %v", duplicate.Code, jsonBody(t, duplicate))
	}

	// The access/users list reports the flags.
	var states []map[string]any
	response = h.getJSON("/admin/access/users?fileId=file-1", h.adminToken)
	if err := decodeInto(t, response, &states); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, state := range states {
		if state["userId"] == h.user.Id {
			found = true
			if state["haveAccess"] != float64(1) || state["owner"] != float64(0) {
				t.Errorf("state = %v", state)
			}
		}
	}
	if !found {
		t.Errorf("user missing from %v", states)
	}

	// Remove the grant.
	remove := h.deleteJSON(t, "/admin/access?fileId=file-1", map[string]any{"ids": []string{h.user.Id}}, h.adminToken)
	if remove.Code != 200 {
		t.Fatalf("remove status = %d, body = %q", remove.Code, remove.Body.String())
	}
	if count, _ := h.store.CountUserAccess("file-1", h.user.Id); count != 0 {
		t.Errorf("access count = %d, want 0", count)
	}
}

func TestAdminAccessPermissions(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.other.Id)

	// A non-owner, non-admin may not read or grant.
	if got := h.getJSON("/admin/access?fileId=file-1", h.userToken); got.Code != http.StatusForbidden {
		t.Errorf("get access status = %d, want 403", got.Code)
	}
	add := h.postJSON(t, "/admin/access/", map[string]string{"fileId": "file-1", "userId": h.user.Id}, h.userToken)
	if add.Code != 400 || jsonBody(t, add)["reason"] != "file-denied" {
		t.Errorf("add = %d %v", add.Code, jsonBody(t, add))
	}

	// Missing files and users.
	missingFile := h.postJSON(t, "/admin/access/", map[string]string{"fileId": "missing", "userId": h.user.Id}, h.adminToken)
	if missingFile.Code != 404 || jsonBody(t, missingFile)["reason"] != "invalid-file-id" {
		t.Errorf("missing file = %d %v", missingFile.Code, jsonBody(t, missingFile))
	}

	noUser := h.postJSON(t, "/admin/access/", map[string]string{"fileId": "file-1"}, h.adminToken)
	if noUser.Code != 400 || jsonBody(t, noUser)["reason"] != "user-cant-be-empty" {
		t.Errorf("no user = %d %v", noUser.Code, jsonBody(t, noUser))
	}
}

func TestAdminDeleteAccessPartial(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.admin.Id)
	h.grantAccess(t, h.user.Id, "file-1")

	response := h.deleteJSON(t, "/admin/access?fileId=file-1", map[string]any{"ids": []string{h.user.Id, "missing"}}, h.adminToken)
	if response.Code != 400 || jsonBody(t, response)["reason"] != "not-all-deleted" {
		t.Errorf("delete = %d %v", response.Code, jsonBody(t, response))
	}
}

func TestAdminTransferOwnership(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.admin.Id)

	response := h.postJSON(t, "/admin/access/transfer-ownership/", map[string]string{
		"fileId": "file-1", "newUserId": h.user.Id,
	}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if got := h.fileRecord(t, "file-1").GetString("owner"); got != h.user.Id {
		t.Errorf("owner = %q, want the new user", got)
	}

	missingUser := h.postJSON(t, "/admin/access/transfer-ownership/", map[string]string{
		"fileId": "file-1", "newUserId": "missing",
	}, h.adminToken)
	if missingUser.Code != 400 || jsonBody(t, missingUser)["reason"] != "new-user-not-found" {
		t.Errorf("missing user = %d %v", missingUser.Code, jsonBody(t, missingUser))
	}

	owned := h.postJSON(t, "/admin/access/transfer-ownership/", map[string]string{
		"fileId": "file-1", "newUserId": h.other.Id,
	}, h.adminToken)
	if owned.Code != 200 {
		t.Errorf("admin transfer status = %d", owned.Code)
	}
}

func (h *harness) mustUser(t *testing.T, id string) *core.Record {
	t.Helper()

	record, err := h.app.FindRecordById("users", id)
	if err != nil {
		t.Fatalf("find user %s: %v", id, err)
	}
	return record
}
