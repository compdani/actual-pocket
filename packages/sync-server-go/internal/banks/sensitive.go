package banks

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// SensitiveOutput receives debug output for the actual-sensitive:* namespaces.
// Tests replace it to capture diagnostics.
var SensitiveOutput io.Writer = os.Stderr

// sensitiveLogf writes a diagnostic only when the DEBUG pattern enables the
// namespace. Arguments are formatted with fmt.Sprintf, so format sequences
// inside an untrusted value are preserved rather than interpreted.
func sensitiveLogf(namespace, format string, args ...any) {
	if !debugNamespaceEnabled(namespace) {
		return
	}
	fmt.Fprintf(SensitiveOutput, "%s\n", fmt.Sprintf(format, args...))
}

func debugNamespaceEnabled(namespace string) bool {
	patterns := strings.Split(os.Getenv("DEBUG"), ",")
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || strings.HasPrefix(pattern, "-") {
			continue
		}
		if pattern == namespace {
			return true
		}
		if strings.HasSuffix(pattern, "*") && strings.HasPrefix(namespace, strings.TrimSuffix(pattern, "*")) {
			return true
		}
		if pattern == "actual-sensitive:*" && strings.HasPrefix(namespace, "actual-sensitive:") {
			return true
		}
	}
	return false
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
