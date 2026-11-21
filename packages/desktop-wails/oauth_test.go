package main

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOAuthCallbackOnlyAcceptsCurrentAttempt(t *testing.T) {
	tokens := make(chan string, 2)
	receiver, err := newOAuthReceiver(func(token string) { tokens <- token })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(receiver.close)
	client := &http.Client{Timeout: 2 * time.Second}
	callback := receiver.url + "/openid-cb"

	for _, invalid := range []struct {
		name, target, host string
		status             int
	}{
		{"wrong attempt", strings.Replace(callback, "/openid-cb", "wrong/openid-cb", 1) + "?token=bad", "", http.StatusNotFound},
		{"missing token", callback, "", http.StatusBadRequest},
		{"rebinding host", callback + "?token=bad", "evil.example", http.StatusNotFound},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, invalid.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			if invalid.host != "" {
				req.Host = invalid.host
			}
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != invalid.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, invalid.status)
			}
			select {
			case token := <-tokens:
				t.Fatalf("untrusted callback delivered token %q", token)
			default:
			}
		})
	}
	token := "test-token+with/slashes="
	response, err := client.Get(callback + "?token=" + url.QueryEscape(token))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if strings.Contains(string(body), token) {
		t.Fatal("callback response leaked token")
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("callback must not cache or leak token URLs")
	}
	select {
	case got := <-tokens:
		if got != token {
			t.Fatalf("token = %q, want %q", got, token)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback was not delivered")
	}
}

func TestExternalURLSchemes(t *testing.T) {
	for _, rawURL := range []string{"https://actualbudget.org/docs", "http://localhost:5006", "mailto:hello@example.com"} {
		if !isExternalURL(rawURL) {
			t.Errorf("rejected normal link %q", rawURL)
		}
	}
	for _, rawURL := range []string{"file:///etc/passwd", "javascript:alert(1)", "actualbudget://localhost", "https://", "https://user:password@example.com", "/local/path"} {
		if isExternalURL(rawURL) {
			t.Errorf("accepted unsafe link %q", rawURL)
		}
	}
}
