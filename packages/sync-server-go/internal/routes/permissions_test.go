package routes

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// permUser is one participant in the permission matrix.
type permUser struct {
	record *core.Record
	token  string
}

var permSeq int64

func nextPermEmail(t *testing.T) string {
	return fmt.Sprintf("perm-%d@example.com", atomic.AddInt64(&permSeq, 1))
}

func (h *harness) newPermUser(t *testing.T, role string) permUser {
	t.Helper()

	email := nextPermEmail(t)
	record := h.createUser(t, email, "password-123", role, true, false)

	return permUser{record: record, token: h.token(t, record)}
}

// permActor builds a user of the requested kind. The owner is the harness'
// admin, because only the first user may be the owner.
func (h *harness) permActor(t *testing.T, kind string) permUser {
	t.Helper()

	switch kind {
	case "owner":
		// Refetch: an earlier case may have changed the owner's password,
		// which rotates tokenKey and invalidates old tokens.
		record, err := h.app.FindRecordById("users", h.admin.Id)
		if err != nil {
			t.Fatal(err)
		}
		return permUser{record: record, token: h.token(t, record)}
	case "admin":
		return h.newPermUser(t, "ADMIN")
	case "basic":
		return h.newPermUser(t, "BASIC")
	}

	t.Fatalf("unknown user kind %q", kind)
	return permUser{}
}

// permTarget is an actor that is also the record being acted on.
func (h *harness) permPair(t *testing.T, actorKind, targetKind string) (permUser, permUser) {
	t.Helper()

	if actorKind == targetKind {
		target := h.permActor(t, targetKind)
		return target, target
	}

	return h.permActor(t, actorKind), h.permActor(t, targetKind)
}

// permAllowed is the hierarchy the rules encode: owner > ADMIN > BASIC, self
// edits without role or enabled changes, and nobody may touch the owner.
func permAllowed(actor, target permUser, op string) bool {
	self := actor.record.Id == target.record.Id
	actorOwner := actor.record.GetBool("owner")
	targetOwner := target.record.GetBool("owner")
	actorRole := actor.record.GetString("role")
	targetRole := target.record.GetString("role")

	editsTarget := actorOwner || (actorRole == "ADMIN" && targetRole == "BASIC")

	switch op {
	case "update-email", "update-password":
		if self {
			return true
		}
		return !targetOwner && editsTarget
	case "change-role":
		if self {
			// An ADMIN may demote itself to BASIC; the owner cannot change its
			// own role.
			return !targetOwner && targetRole == "ADMIN"
		}
		return !targetOwner && actorOwner
	case "disable", "delete":
		return !self && !targetOwner && editsTarget
	}

	return false
}

// TestUserPermissionMatrix runs every actor/target/operation combination through
// both our /admin/users routes and PocketBase's own users endpoints.
func TestUserPermissionMatrix(t *testing.T) {
	h := newHarness(t)

	// PocketBase's own rate limiter would throttle this many create requests.
	settings := h.app.Settings()
	settings.RateLimits.Enabled = false
	if err := h.app.Save(settings); err != nil {
		t.Fatal(err)
	}

	actorKinds := []string{"owner", "admin", "basic"}
	targetKinds := []string{"owner", "admin", "basic"}
	operations := []string{"update-email", "update-password", "change-role", "disable", "delete"}

	for _, actorKind := range actorKinds {
		for _, targetKind := range targetKinds {
			for _, op := range operations {
				name := fmt.Sprintf("%s/%s/%s", actorKind, targetKind, op)
				t.Run(name, func(t *testing.T) {
					want := permAllowedForKinds(t, h, actorKind, targetKind, op)

					// PocketBase first: the owner is shared between the two
					// surfaces, and a self password change needs the old one.
					t.Run("pocketbase api", func(t *testing.T) {
						actor, target := h.permPair(t, actorKind, targetKind)
						status := h.runPocketBaseUserOp(t, actor, target, op)

						allowed := status < 400
						if op == "update-email" && actor.record.Id == target.record.Id {
							// PocketBase refuses direct self email changes (they go
							// through its email-change confirmation flow), but the
							// rule allows it: check the rule itself.
							if want && status == http.StatusBadRequest {
								allowed = h.ruleAllowsUpdate(t, actor, target, op)
							}
						}

						if allowed != want {
							t.Fatalf("allowed = %v (status %d), want %v", allowed, status, want)
						}
						if !want && status != http.StatusForbidden && status != http.StatusNotFound {
							t.Fatalf("denied with status %d, want 403 or 404", status)
						}
					})

					t.Run("admin route", func(t *testing.T) {
						actor, target := h.permPair(t, actorKind, targetKind)
						status := h.runAdminUserOp(t, actor, target, op)
						want := want && (actor.record.Id != target.record.Id || (op != "update-email" && op != "update-password"))
						if got := status == http.StatusOK; got != want {
							t.Fatalf("allowed = %v (status %d), want %v", got, status, want)
						}
					})
				})
			}
		}
	}
}

func permAllowedForKinds(t *testing.T, h *harness, actorKind, targetKind, op string) bool {
	t.Helper()

	// The decision only depends on role and ownership, so it can be computed
	// without creating the users.
	actor := permUser{record: core.NewRecord(h.mustCollection(t, "users"))}
	target := permUser{record: core.NewRecord(h.mustCollection(t, "users"))}
	// NewRecord leaves the id empty until save; self-detection needs ids.
	actor.record.Id = "actor"
	target.record.Id = "target"
	if actorKind == targetKind {
		// The matrix acts on itself when the kinds match.
		target.record.Id = actor.record.Id
	}

	actor.record.Set("role", roleForKind(h, actorKind))
	actor.record.Set("owner", actorKind == "owner")
	target.record.Set("role", roleForKind(h, targetKind))
	target.record.Set("owner", targetKind == "owner")

	return permAllowed(actor, target, op)
}

func roleForKind(h *harness, kind string) string {
	if kind == "basic" {
		return "BASIC"
	}
	return "ADMIN"
}

func (h *harness) mustCollection(t *testing.T, name string) *core.Collection {
	t.Helper()

	collection, err := h.app.FindCollectionByNameOrId(name)
	if err != nil {
		t.Fatal(err)
	}
	return collection
}

// runAdminUserOp performs the operation through our /admin/users routes and
// returns the status code.
func (h *harness) runAdminUserOp(t *testing.T, actor, target permUser, op string) int {
	t.Helper()

	base := map[string]any{
		"id":          target.record.Id,
		"userName":    target.record.GetString("email"),
		"displayName": target.record.GetString("name"),
		"role":        target.record.GetString("role"),
		"enabled":     target.record.GetBool("enabled"),
	}

	var response *httptest.ResponseRecorder

	switch op {
	case "update-email":
		base["userName"] = nextPermEmail(t)
		response = h.patchJSON(t, "/admin/users/", base, actor.token)
	case "update-password":
		base["password"] = "new-password-123"
		response = h.patchJSON(t, "/admin/users/", base, actor.token)
	case "change-role":
		base["role"] = oppositeRole(target.record.GetString("role"))
		response = h.patchJSON(t, "/admin/users/", base, actor.token)
	case "disable":
		base["enabled"] = false
		response = h.patchJSON(t, "/admin/users/", base, actor.token)
	case "delete":
		response = h.deleteJSON(t, "/admin/users", map[string]any{"ids": []string{target.record.Id}}, actor.token)
	default:
		t.Fatalf("unknown op %q", op)
	}

	return response.Code
}

// runPocketBaseUserOp performs the operation through PocketBase's own REST API.
func (h *harness) runPocketBaseUserOp(t *testing.T, actor, target permUser, op string) int {
	t.Helper()

	path := "/api/collections/users/records/" + target.record.Id

	switch op {
	case "update-email":
		return h.pbRequest(t, http.MethodPatch, path, map[string]any{
			"email": nextPermEmail(t),
		}, actor.token).Code
	case "update-password":
		body := map[string]any{
			"password":        "new-password-123",
			"passwordConfirm": "new-password-123",
		}
		if actor.record.Id == target.record.Id {
			body["oldPassword"] = "password-123"
			if actor.record.GetBool("owner") {
				body["oldPassword"] = "admin-password"
			}
		}
		response := h.pbRequest(t, http.MethodPatch, path, body, actor.token)
		if response.Code >= 400 {
			t.Logf("PB password change failed: %d %s", response.Code, response.Body.String())
		}
		return response.Code
	case "change-role":
		return h.pbRequest(t, http.MethodPatch, path, map[string]any{
			"role": oppositeRole(target.record.GetString("role")),
		}, actor.token).Code
	case "disable":
		return h.pbRequest(t, http.MethodPatch, path, map[string]any{"enabled": false}, actor.token).Code
	case "delete":
		return h.pbRequest(t, http.MethodDelete, path, nil, actor.token).Code
	}

	t.Fatalf("unknown op %q", op)
	return 0
}

// ruleAllowsUpdate reports the raw update-rule decision for a case where
// PocketBase's form adds a requirement of its own.
func (h *harness) ruleAllowsUpdate(t *testing.T, actor, target permUser, op string) bool {
	t.Helper()

	record, err := h.app.FindRecordById("users", target.record.Id)
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{}
	switch op {
	case "update-email":
		body["email"] = nextPermEmail(t)
	case "update-password":
		body["password"] = "new-password-123"
	}

	allowed, err := h.app.CanAccessRecord(record, &core.RequestInfo{
		Auth:   actor.record,
		Body:   body,
		Method: http.MethodPatch,
	}, record.Collection().UpdateRule)
	if err != nil {
		t.Fatal(err)
	}

	return allowed
}

func oppositeRole(role string) string {
	if role == "BASIC" {
		return "ADMIN"
	}
	return "BASIC"
}

// TestUserCreatePermissionMatrix covers creation with each role and the
// server-owned fields.
func TestUserCreatePermissionMatrix(t *testing.T) {
	h := newHarness(t)

	settings := h.app.Settings()
	settings.RateLimits.Enabled = false
	if err := h.app.Save(settings); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		actorKind string
		role      string
		extra     map[string]any
		want      bool
	}{
		{"owner creates basic", "owner", "BASIC", nil, true},
		{"owner creates admin", "owner", "ADMIN", nil, true},
		{"admin creates basic", "admin", "BASIC", nil, true},
		{"admin creates admin", "admin", "ADMIN", nil, false},
		{"basic creates basic", "basic", "BASIC", nil, false},
		{"owner creates owner", "owner", "BASIC", map[string]any{"owner": true}, false},
		{"admin creates owner", "admin", "BASIC", map[string]any{"owner": true}, false},
		{"owner sets password_set", "owner", "BASIC", map[string]any{"password_set": true}, false},
		{"admin sets password_set", "admin", "BASIC", map[string]any{"password_set": true}, false},
		{"owner sends owner false", "owner", "BASIC", map[string]any{"owner": false}, false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Run("admin route", func(t *testing.T) {
				actor := h.permActor(t, testCase.actorKind)
				status := h.runAdminCreate(t, actor, testCase.role, testCase.extra)
				if got := status == http.StatusOK; got != testCase.want {
					t.Fatalf("allowed = %v (status %d), want %v", got, status, testCase.want)
				}
			})

			t.Run("pocketbase api", func(t *testing.T) {
				actor := h.permActor(t, testCase.actorKind)
				status := h.runPocketBaseCreate(t, actor, testCase.role, testCase.extra)
				if got := status < 400; got != testCase.want {
					t.Fatalf("allowed = %v (status %d), want %v", got, status, testCase.want)
				}
				if !testCase.want && status != http.StatusForbidden && status != http.StatusBadRequest {
					t.Fatalf("denied with status %d", status)
				}
			})
		})
	}
}

func (h *harness) runAdminCreate(t *testing.T, actor permUser, role string, extra map[string]any) int {
	t.Helper()

	body := map[string]any{
		"userName":    nextPermEmail(t),
		"displayName": "Created",
		"role":        role,
		"enabled":     true,
		"password":    "created-password",
	}
	for key, value := range extra {
		body[key] = value
	}

	return h.postJSON(t, "/admin/users/", body, actor.token).Code
}

func TestPocketBaseCreateWithPasswordSetsPasswordSet(t *testing.T) {
	h := newHarness(t)
	owner := h.permActor(t, "owner")

	email := nextPermEmail(t)
	body := map[string]any{
		"email":           email,
		"name":            "Created",
		"role":            "BASIC",
		"enabled":         true,
		"password":        "created-password",
		"passwordConfirm": "created-password",
	}
	res := h.pbRequest(t, http.MethodPost, "/api/collections/users/records", body, owner.token)
	if res.Code != http.StatusOK {
		t.Fatalf("create via PocketBase API: status %d, body %s", res.Code, res.Body.String())
	}

	record, err := h.app.FindAuthRecordByEmail("users", email)
	if err != nil {
		t.Fatalf("find created user: %v", err)
	}
	if !record.GetBool("password_set") {
		t.Error("password_set = false for a user created with a password through the PocketBase API, want true")
	}
}

func (h *harness) runPocketBaseCreate(t *testing.T, actor permUser, role string, extra map[string]any) int {
	t.Helper()

	body := map[string]any{
		"email":           nextPermEmail(t),
		"name":            "Created",
		"role":            role,
		"enabled":         true,
		"password":        "created-password",
		"passwordConfirm": "created-password",
	}
	for key, value := range extra {
		body[key] = value
	}

	return h.pbRequest(t, http.MethodPost, "/api/collections/users/records", body, actor.token).Code
}
