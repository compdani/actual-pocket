package routes

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/banks"
)

func TestSimpleFINStaleKeysReclaim(t *testing.T) {
	for name, stale := range map[string]string{
		"empty":     "",
		"not-a-url": "<html>Bad Gateway</html>",
	} {
		t.Run(name, func(t *testing.T) {
			h, fake := newSimpleFINHarness(t, true)
			token := fake.setToken(t)
			if err := h.store.SetSecret("simplefin_token", "", token); err != nil {
				t.Fatal(err)
			}
			if err := h.store.SetSecret("simplefin_accessKey", "", stale); err != nil {
				t.Fatal(err)
			}
			valid := simplefinAccessKey(fake, "user", "password")
			fake.claimBody = valid + "\n"

			response := h.postJSON(t, "/simplefin/accounts", nil, h.userToken)
			if response.Code != 200 || fake.count("/claim") != 1 {
				t.Fatalf("status = %d, claims = %d, body = %q", response.Code, fake.count("/claim"), response.Body.String())
			}
			if stored, _, _ := h.store.Secret("simplefin_accessKey", ""); stored != valid {
				t.Errorf("stored key = %q", stored)
			}
		})
	}
}

func TestSimpleFINBudgetClaimDoesNotTouchGlobals(t *testing.T) {
	h, fake := newSimpleFINHarness(t, true)
	globalAccess := "https://global:password@bridge.example"
	if err := h.store.SetSecret("simplefin_token", "", "global-token"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSecret("simplefin_accessKey", "", globalAccess); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSecret("simplefin_token", "file-1", fake.setToken(t)); err != nil {
		t.Fatal(err)
	}

	budgetAccess := "https://budget:password@bridge.example"
	fake.claimBody = budgetAccess + "\n"
	response := h.postJSONWithFile(t, "/simplefin/accounts", nil, h.adminToken, "file-1")
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if stored, _, _ := h.store.Secret("simplefin_accessKey", "file-1"); stored != budgetAccess {
		t.Errorf("budget key = %q", stored)
	}
	if stored, _, _ := h.store.Secret("simplefin_accessKey", ""); stored != globalAccess {
		t.Errorf("global key was replaced: %q", stored)
	}
}

func TestSimpleFINInvalidBudgetTokenDoesNotFallBack(t *testing.T) {
	h, fake := newSimpleFINHarness(t, true)
	if err := h.store.SetSecret("simplefin_token", "", "global-token"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSecret("simplefin_accessKey", "", simplefinAccessKey(fake, "global", "password")); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSecret("simplefin_token", "file-1", "Forbidden"); err != nil {
		t.Fatal(err)
	}

	data := jsonBody(t, h.postJSONWithFile(t, "/simplefin/status", nil, h.adminToken, "file-1"))["data"].(map[string]any)
	if data["configured"] != false || data["source"] != "per-budget-file" {
		t.Errorf("status = %v", data)
	}
	response := h.postJSONWithFile(t, "/simplefin/accounts", nil, h.adminToken, "file-1")
	if data := jsonBody(t, response)["data"].(map[string]any); data["error_code"] != "INVALID_ACCESS_TOKEN" {
		t.Errorf("accounts = %v", data)
	}
	if fake.count("/claim") != 0 || fake.count("/accounts") != 0 {
		t.Error("budget token fell back to global credentials")
	}
}

func TestSimpleFINClearedBudgetFallsBackToGlobal(t *testing.T) {
	h, fake := newSimpleFINHarness(t, true)
	if err := h.store.SetSecret("simplefin_token", "", fake.setToken(t)); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSecret("simplefin_token", "file-1", "budget-token"); err != nil {
		t.Fatal(err)
	}

	data := jsonBody(t, h.postJSONWithFile(t, "/simplefin/status", nil, h.adminToken, "file-1"))["data"].(map[string]any)
	if data["source"] != "per-budget-file" {
		t.Fatalf("budget source = %v", data)
	}
	if err := h.store.ResetSecret("simplefin_token", "file-1"); err != nil {
		t.Fatal(err)
	}
	data = jsonBody(t, h.postJSONWithFile(t, "/simplefin/status", nil, h.adminToken, "file-1"))["data"].(map[string]any)
	if data["source"] != "global" || data["configured"] != true {
		t.Errorf("global fallback = %v", data)
	}
}

func TestSimpleFINSensitiveDiagnostics(t *testing.T) {
	h, fake := newSimpleFINHarness(t, true)
	if err := h.store.SetSecret("simplefin_token", "", fake.setToken(t)); err != nil {
		t.Fatal(err)
	}

	buffer := &bytes.Buffer{}
	previous := banks.SensitiveOutput
	banks.SensitiveOutput = buffer
	t.Cleanup(func() { banks.SensitiveOutput = previous })
	t.Setenv("DEBUG", "actual-sensitive:simplefin")
	fake.claimBody = "Rejected: %O %s %%"

	response := h.postJSON(t, "/simplefin/accounts", nil, h.userToken)
	if data := jsonBody(t, response)["data"].(map[string]any); data["error_code"] != "INVALID_ACCESS_TOKEN" {
		t.Fatalf("data = %v", data)
	}
	if !bytes.Contains(buffer.Bytes(), []byte("Rejected: %O %s %%")) {
		t.Errorf("diagnostic lost format sequences: %q", buffer.String())
	}
}

func TestSimpleFINTransitionAddresses(t *testing.T) {
	for name, url := range map[string]string{
		"nat64 metadata": "http://[64:ff9b::a9fe:a9fe]/claim",
		"6to4":           "http://[2002:a9fe:a9fe::]/claim",
	} {
		t.Run(name, func(t *testing.T) {
			h, fake := newSimpleFINHarness(t, true)
			token := base64.StdEncoding.EncodeToString([]byte(url))
			if err := h.store.SetSecret("simplefin_token", "", token); err != nil {
				t.Fatal(err)
			}
			response := h.postJSON(t, "/simplefin/accounts", nil, h.userToken)
			if data := jsonBody(t, response)["data"].(map[string]any); data["error_code"] != "SERVER_DOWN" {
				t.Errorf("data = %v", data)
			}
			if fake.count("/claim") != 0 {
				t.Error("contacted a transition address")
			}
		})
	}

	t.Run("redirect", func(t *testing.T) {
		h, fake := newSimpleFINHarness(t, true)
		if err := h.store.SetSecret("simplefin_accessKey", "", simplefinAccessKey(fake, "user", "password")); err != nil {
			t.Fatal(err)
		}
		fake.redirect = "http://[64:ff9b::a9fe:a9fe]/accounts"
		response := h.postJSON(t, "/simplefin/accounts", nil, h.userToken)
		if data := jsonBody(t, response)["data"].(map[string]any); data["error_code"] != "SERVER_DOWN" {
			t.Errorf("data = %v", data)
		}
	})
}
