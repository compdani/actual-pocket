package main

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// xdgDocumentsDir reads XDG_DOCUMENTS_DIR on Linux, from the environment or
// from user-dirs.dirs, as Electron's app.getPath('documents') does.
func xdgDocumentsDir(homeDir string) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	if dir := os.Getenv("XDG_DOCUMENTS_DIR"); dir != "" {
		return expandHome(dir, homeDir)
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(homeDir, ".config")
	}
	file, err := os.Open(filepath.Join(configHome, "user-dirs.dirs"))
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, found := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if !found || key != "XDG_DOCUMENTS_DIR" {
			continue
		}
		return expandHome(strings.Trim(value, `"`), homeDir)
	}
	return ""
}

func expandHome(path, homeDir string) string {
	if rest, ok := strings.CutPrefix(path, "$HOME"); ok {
		return filepath.Join(homeDir, rest)
	}
	return path
}

// sidecarPath is where a file shipped next to the executable lives: inside
// Contents/MacOS for the macOS bundle, beside the .exe or binary elsewhere.
func sidecarPath(name string) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return filepath.Join(filepath.Dir(executable), name), nil
}

// resourcePath is where bundled data lives: Contents/Resources on macOS,
// a `resources` folder beside the executable elsewhere.
func resourcePath(name string) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	dir := filepath.Dir(executable)
	if runtime.GOOS == "darwin" {
		return filepath.Join(dir, "..", "Resources", name), nil
	}
	return filepath.Join(dir, "resources", name), nil
}
