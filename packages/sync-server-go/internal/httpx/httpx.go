// Package httpx provides response helpers that mimic Express's `res.send`
// semantics, because the Actual web client was written against them.
package httpx

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

const (
	// ContentTypeJSON matches `res.send(object)` / `res.json()`.
	ContentTypeJSON = "application/json; charset=utf-8"
	// ContentTypeText matches `res.send('string')`: Express defaults that to HTML.
	ContentTypeText = "text/html; charset=utf-8"
)

// JSON writes `data` as an Express JSON response: the JSON content type with a
// charset, and no trailing newline.
func JSON(e *core.RequestEvent, status int, data any) error {
	body, err := Marshal(data)
	if err != nil {
		return err
	}
	return writeBody(e, status, ContentTypeJSON, body)
}

// Text writes a plain string body, exactly like `res.send('string')`: Express
// labels string bodies as text/html.
func Text(e *core.RequestEvent, status int, body string) error {
	return writeBody(e, status, ContentTypeText, []byte(body))
}

// Error writes the Node server's unhandled-error response.
func Error(e *core.RequestEvent) error {
	return JSON(e, 500, errorBody{Status: "error", Reason: "internal-error"})
}

// errorBody keeps Express's field order: JSON.stringify preserves the order of
// the object literal, and Go maps are sorted.
type errorBody struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// Marshal encodes v the way JSON.stringify does: compact, and without escaping
// <, > and & (Go's encoder does by default).
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// writeBody sends a response the way Express's `res.send` does, including the
// weak ETag (and the 304 it enables).
func writeBody(e *core.RequestEvent, status int, contentType string, body []byte) error {
	header := e.Response.Header()
	tag := ETag(body)
	header.Set("ETag", tag)

	if isFresh(e.Request.Header.Get("If-None-Match"), tag) {
		header.Del("Content-Type")
		header.Del("Content-Length")
		e.Response.WriteHeader(304)
		return nil
	}

	header.Set("Content-Type", contentType)
	e.Response.WriteHeader(status)

	_, err := e.Response.Write(body)
	return err
}

// ETag builds Express's default weak ETag: the body length in hex, a dash, and
// the first 27 characters of the base64 SHA-1 of the body.
func ETag(body []byte) string {
	sum := sha1.Sum(body)
	hash := base64.StdEncoding.EncodeToString(sum[:])
	if len(hash) > 27 {
		hash = hash[:27]
	}

	return `W/"` + strconv.FormatInt(int64(len(body)), 16) + "-" + hash + `"`
}

// isFresh reports whether If-None-Match matches, using the weak comparison
// Express applies before answering 304.
func isFresh(ifNoneMatch, tag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	if strings.TrimSpace(ifNoneMatch) == "*" {
		return true
	}
	for candidate := range strings.SplitSeq(ifNoneMatch, ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == strings.TrimPrefix(tag, "W/") {
			return true
		}
	}
	return false
}
