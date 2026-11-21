package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFrontendReloadsAndOAuthCallback(t *testing.T) {
	assets := fstest.MapFS{
		"index.html": {Data: []byte("<!DOCTYPE html><div id=\"root\"></div>")},
	}
	handler := frontendHandler(assets)
	for _, target := range []string{"/", "/budget", "/accounts/test-account", "/openid-cb?token=test"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "id=\"root\"") {
			t.Errorf("%s: status %d, body %q", target, response.Code, response.Body.String())
		}
	}
	for _, target := range []string{"/static/missing.js", "/kcab/missing", "/data/missing", "/missing.wasm"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("missing asset %s returned %d", target, response.Code)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/wails/runtime.js", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "javascript") {
		t.Fatal("Wails runtime was not served as JavaScript")
	}
}
