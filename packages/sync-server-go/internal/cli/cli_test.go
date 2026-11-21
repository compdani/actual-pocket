package cli

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
	_ "github.com/compdani/actual-pocket/packages/sync-server-go/migrations"
)

func newCLIApp(t *testing.T) (*pocketbase.PocketBase, *config.Config) {
	t.Helper()

	cfg := &config.Config{
		Hostname: "127.0.0.1",
		Port:     5006,
	}

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  t.TempDir() + "/pb_data",
		HideStartBanner: true,
	})
	Register(app, cfg)

	return app, cfg
}

func runCommand(t *testing.T, app *pocketbase.PocketBase, input string, args ...string) error {
	t.Helper()

	previousIn, previousOut := stdin, stdout
	stdin = strings.NewReader(input)
	stdout = io.Discard
	t.Cleanup(func() {
		stdin = previousIn
		stdout = previousOut
	})

	app.RootCmd.SetArgs(args)
	return app.RootCmd.Execute()
}

func TestCreateOwner(t *testing.T) {
	app, _ := newCLIApp(t)

	if err := runCommand(t, app, "owner-password\nowner-password\n", "create-owner", "owner@example.com"); err != nil {
		t.Fatalf("create-owner: %v", err)
	}

	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })

	record, err := app.FindAuthRecordByEmail("users", "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !record.GetBool("owner") || record.GetString("role") != "ADMIN" || !record.GetBool("enabled") {
		t.Errorf("owner record = %v", record)
	}
	if !record.GetBool("password_set") {
		t.Error("password_set = false after create-owner")
	}
	if !record.ValidatePassword("owner-password") {
		t.Error("password does not validate")
	}
}

func TestCreateOwnerRefusesExistingUsers(t *testing.T) {
	app, _ := newCLIApp(t)

	if err := runCommand(t, app, "owner-password\nowner-password\n", "create-owner", "owner@example.com"); err != nil {
		t.Fatalf("first create-owner: %v", err)
	}

	// No input is consumed because the command refuses before prompting.
	err := runCommand(t, app, "", "create-owner", "second@example.com")
	if err == nil || !strings.Contains(err.Error(), "already has users") {
		t.Fatalf("second create-owner error = %v", err)
	}
}

func TestCreateOwnerRejectsMismatch(t *testing.T) {
	app, _ := newCLIApp(t)

	err := runCommand(t, app, "one-password\ntwo-password\n", "create-owner", "owner@example.com")
	if err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("error = %v", err)
	}
}

func TestResetPassword(t *testing.T) {
	app, _ := newCLIApp(t)

	if err := runCommand(t, app, "owner-password\nowner-password\n", "create-owner", "owner@example.com"); err != nil {
		t.Fatalf("create-owner: %v", err)
	}

	if err := runCommand(t, app, "new-password\nnew-password\n", "reset-password", "owner@example.com"); err != nil {
		t.Fatalf("reset-password: %v", err)
	}

	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })

	record, err := app.FindAuthRecordByEmail("users", "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !record.ValidatePassword("new-password") {
		t.Error("new password does not validate")
	}
	if record.ValidatePassword("owner-password") {
		t.Error("old password still validates")
	}
	if !record.GetBool("password_set") {
		t.Error("password_set = false after reset-password")
	}
}

func TestResetPasswordUnknownUser(t *testing.T) {
	app, _ := newCLIApp(t)

	err := runCommand(t, app, "", "reset-password", "missing@example.com")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestHealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"UP"}`))
	}))
	t.Cleanup(server.Close)

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}

	app, cfg := newCLIApp(t)
	cfg.Hostname = parsed.Hostname()
	cfg.Port = port

	if err := runCommand(t, app, "", "health-check"); err != nil {
		t.Fatalf("UP health check: %v", err)
	}

	down, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, downPort, _ := net.SplitHostPort(down.Addr().String())
	_ = down.Close()
	cfg.Port, _ = strconv.Atoi(downPort)

	if err := runCommand(t, app, "", "health-check"); err == nil {
		t.Fatal("DOWN health check returned nil")
	}
}
