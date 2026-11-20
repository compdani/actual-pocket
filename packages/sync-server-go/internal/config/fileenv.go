package config

import (
	"fmt"
	"os"
	"strings"
)

// readFileEnv implements the `<VAR>_FILE` convention: the variable holds a path
// whose contents are used instead of the plain variable. It is a port of
// packages/sync-server/src/config-file-env.ts.
func readFileEnv(env map[string]string, fileEnvVar string) (string, bool, error) {
	path, present := env[fileEnvVar]
	if !present {
		return "", false, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("could not read %s from %q: %w", fileEnvVar, path, err)
	}

	return strings.TrimSpace(string(raw)), true, nil
}
