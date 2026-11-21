package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The one-use loopback listener receives the server's token after OAuth in
// the system browser. A random path binds it to this specific login attempt.
type oauthReceiver struct {
	server *http.Server
	url    string
	once   sync.Once
	timer  *time.Timer
}

func newOAuthReceiver(onToken func(string)) (*oauthReceiver, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		listener.Close()
		return nil, err
	}
	path := "/" + hex.EncodeToString(nonce)
	port := listener.Addr().(*net.TCPAddr).Port
	host := fmt.Sprintf("localhost:%d", port)
	receiver := &oauthReceiver{url: "http://" + host + path}
	receiver.server = &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if r.Method != http.MethodGet || r.Host != host ||
				subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte(path+"/openid-cb")) != 1 {
				http.NotFound(w, r)
				return
			}
			token := r.URL.Query().Get("token")
			if token == "" || len(token) > 16384 {
				http.Error(w, "Missing or invalid token", http.StatusBadRequest)
				return
			}
			accepted := false
			receiver.once.Do(func() {
				accepted = true
				// Pass only the token to the application; never navigate it to
				// a URL supplied by the request or log the callback query.
				onToken(token)
				go receiver.close()
			})
			if !accepted {
				http.Error(w, "Login already completed", http.StatusGone)
				return
			}
			fmt.Fprintln(w, "Sign-in complete. You can return to Actual Pocket.")
		}),
	}
	receiver.timer = time.AfterFunc(5*time.Minute, receiver.close)
	go func() { _ = receiver.server.Serve(listener) }()
	return receiver, nil
}

func (o *oauthReceiver) close() {
	if o.timer != nil {
		o.timer.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = o.server.Shutdown(ctx)
}

func (d *Desktop) StartOAuthServer() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.oauth != nil {
		d.oauth.close()
	}
	receiver, err := newOAuthReceiver(func(token string) {
		callback := "/openid-cb?" + url.Values{"token": {token}}.Encode()
		d.window.SetURL(callback)
		d.window.Show()
		d.window.Focus()
	})
	if err != nil {
		return "", err
	}
	d.oauth = receiver
	return strings.TrimSuffix(receiver.url, "/"), nil
}
