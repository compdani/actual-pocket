package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newDesktopWithFolder(t *testing.T) (*Desktop, string) {
	t.Helper()
	settings, err := loadSettings(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(t.TempDir(), "Actual")
	return &Desktop{settings: settings, defaultDocumentDir: current}, current
}

func TestMoveBudgetDirectory(t *testing.T) {
	desktop, current := newDesktopWithFolder(t)
	budget := filepath.Join(current, "My-Budget-abc")
	if err := os.MkdirAll(filepath.Join(budget, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	for name, contents := range map[string]string{"db.sqlite": "db", "metadata.json": "{}", "backups/1.zip": "zip"} {
		path := filepath.Join(budget, name)
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	target := t.TempDir()
	if err := desktop.MoveBudgetDirectory(target); err != nil {
		t.Fatal(err)
	}
	if desktop.GetDocumentDir() != target {
		t.Fatalf("folder not switched: %s", desktop.GetDocumentDir())
	}
	info, err := os.Stat(filepath.Join(target, "My-Budget-abc", "backups", "1.zip"))
	if err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("backup not copied with its date: %v %v", info, err)
	}
	if _, err := os.Stat(budget); !os.IsNotExist(err) {
		t.Fatalf("old budget left behind: %v", err)
	}
}

func TestMoveBudgetDirectoryRefusals(t *testing.T) {
	desktop, current := newDesktopWithFolder(t)
	if err := os.MkdirAll(filepath.Join(current, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(current, "inside")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := desktop.MoveBudgetDirectory(inside); err == nil {
		t.Fatal("moved into a folder inside the current one")
	}
	if err := desktop.MoveBudgetDirectory(filepath.Join(current, "missing")); err == nil {
		t.Fatal("moved into a missing folder")
	}
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := desktop.MoveBudgetDirectory(target); err == nil {
		t.Fatal("overwrote a budget already in the new folder")
	}
	if desktop.GetDocumentDir() != current {
		t.Fatal("folder switched after a refused move")
	}
}

func TestSetDocumentDir(t *testing.T) {
	desktop, _ := newDesktopWithFolder(t)
	if err := desktop.SetDocumentDir("relative/path"); err == nil {
		t.Fatal("accepted a relative path")
	}
	target := t.TempDir()
	if err := desktop.SetDocumentDir(target); err != nil {
		t.Fatal(err)
	}
	if desktop.GetDocumentDir() != target {
		t.Fatal("folder not switched")
	}
}

func TestOpenInFileManagerRefusesRemotePaths(t *testing.T) {
	desktop := &Desktop{}
	for _, path := range []string{"relative.txt", "//server/share/file", "https://example.com"} {
		if err := desktop.OpenInFileManager(path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
}

func TestCheckCertificateFile(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "budget.local"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pemPath := filepath.Join(dir, "server.pem")
	derPath := filepath.Join(dir, "server.cer")
	keyPath := filepath.Join(dir, "server.key")
	_ = os.WriteFile(pemPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(derPath, der, 0o600)
	_ = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("x")}), 0o600)
	if err := checkCertificateFile(pemPath); err != nil {
		t.Errorf("PEM certificate refused: %v", err)
	}
	if err := checkCertificateFile(derPath); err != nil {
		t.Errorf("DER certificate refused: %v", err)
	}
	if err := checkCertificateFile(keyPath); err == nil {
		t.Error("private key accepted as a certificate")
	}
}
