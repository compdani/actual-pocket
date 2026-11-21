package routes

import "testing"

func TestSimpleFINOverrideRequiresTestMode(t *testing.T) {
	fake := newFakeSimpleFIN(t)
	t.Setenv("ACTUAL_TEST_MODE", "0")
	t.Setenv("ACTUAL_TEST_SIMPLEFIN_BASE_URL", fake.server.URL)

	h := newHarness(t)
	if err := h.store.SetSecret("simplefin_accessKey", "", simplefinAccessKey(fake, "user", "password")); err != nil {
		t.Fatal(err)
	}

	response := h.postJSON(t, "/simplefin/transactions", map[string]any{
		"accountId": "account-1",
		"startDate": "2026-01-01",
	}, h.userToken)
	data := jsonBody(t, response)["data"].(map[string]any)
	if data["error_code"] != "SERVER_DOWN" {
		t.Errorf("data = %v", data)
	}
	if fake.count("/accounts") != 0 {
		t.Error("override was used without ACTUAL_TEST_MODE")
	}
}

func TestSimpleFINOverrideActive(t *testing.T) {
	fake := newFakeSimpleFIN(t)
	t.Setenv("ACTUAL_TEST_MODE", "1")
	t.Setenv("ACTUAL_TEST_SIMPLEFIN_BASE_URL", fake.server.URL)

	h := newHarness(t)
	if err := h.store.SetSecret("simplefin_accessKey", "", "https://user:pass@bridge.example"); err != nil {
		t.Fatal(err)
	}

	response := h.postJSON(t, "/simplefin/transactions", map[string]any{
		"accountId": "account-1",
		"startDate": "2026-01-01",
	}, h.userToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if fake.count("/accounts") != 1 {
		t.Errorf("fake /accounts calls = %d, want 1", fake.count("/accounts"))
	}
}
