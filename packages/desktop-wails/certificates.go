package main

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// The Electron app trusted a self-signed server certificate inside its own
// Node backend. Here requests come from the system WebView, which trusts
// what the operating system trusts, so the certificate is added to the
// current user's trust store. The OS asks the user to confirm.

// TrustCertificate asks for a certificate file and adds it to the user's
// trust store. It returns "" when the dialog was cancelled, and otherwise a
// short message for the user.
func (d *Desktop) TrustCertificate() (string, error) {
	path, err := d.app.Dialog.OpenFile().
		CanChooseFiles(true).
		AddFilter("Certificate", "*.crt;*.pem;*.cer").
		SetTitle("Choose the server's certificate").
		AttachToWindow(d.window).
		PromptForSingleSelection()
	if err != nil || path == "" {
		return "", err
	}
	if err := checkCertificateFile(path); err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		keychain := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
		output, err := exec.Command("security", "add-trusted-cert", "-r", "trustRoot", "-k", keychain, path).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("could not add the certificate to your keychain: %s", strings.TrimSpace(string(output)))
		}
	case "windows":
		output, err := exec.Command("certutil", "-user", "-addstore", "Root", path).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("could not add the certificate to your certificate store: %s", strings.TrimSpace(string(output)))
		}
	default:
		return "", errors.New("on Linux, add the certificate to the system's trusted certificates (for example with update-ca-certificates) and restart the app")
	}
	return "The certificate is now trusted. Connect to the server again.", nil
}

// checkCertificateFile refuses anything that is not a PEM or DER
// certificate, so a wrong file never reaches the trust store.
func checkCertificateFile(path string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	der := contents
	if block, _ := pem.Decode(contents); block != nil {
		if block.Type != "CERTIFICATE" {
			return errors.New("that file does not contain a certificate")
		}
		der = block.Bytes
	}
	if _, err := x509.ParseCertificate(der); err != nil {
		return errors.New("that file does not contain a certificate")
	}
	return nil
}
