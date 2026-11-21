package routes

import (
	"net/http"
	"testing"
)

func TestNotPortedBankSyncStatus(t *testing.T) {
	h := newHarness(t)

	for _, provider := range []string{"simplefin", "enablebanking", "pluggyai", "akahu", "gocardless"} {
		response := h.postJSON(t, "/"+provider+"/status", nil, h.adminToken)
		if response.Code != 200 {
			t.Fatalf("%s status = %d, body = %q", provider, response.Code, response.Body.String())
		}
		if got, want := response.Body.String(), `{"status":"ok","data":{"configured":false}}`; got != want {
			t.Errorf("%s body = %q, want %q", provider, got, want)
		}
	}

	if got := h.postJSON(t, "/simplefin/status", nil, ""); got.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d, want 401", got.Code)
	}
}
