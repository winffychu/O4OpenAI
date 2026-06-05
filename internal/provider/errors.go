package provider

import (
	"errors"
	"fmt"
	"strings"
)

// ============================================================
// Provider-level error types
//
// These carry the upstream HTTP status code so handlers can
// surface a meaningful response (e.g. 401 for missing/invalid
// API key) instead of always returning 500.
//
// Security note: the upstream body is treated as untrusted.
//   - Logs see a TRUNCATED version (first maxBodyLogBytes)
//   - Clients see ONLY the safe summary, never the raw body
// ============================================================

// maxBodyLogBytes is how much of an upstream error body to include in logs.
const maxBodyLogBytes = 200

// maxBodyClientBytes is how much of an upstream error body to expose to clients.
const maxBodyClientBytes = 120

// StatusCodeForError maps a Go error to a sensible HTTP status code.
// Returns 500 by default, 401 for missing/invalid API key.
func StatusCodeForError(err error) int {
	if err == nil {
		return 200
	}
	// Missing key takes priority
	if errors.Is(err, ErrNoAPIKey) {
		return 401
	}
	// Upstream error with a specific code
	var pe *ProviderError
	if errors.As(err, &pe) && pe.StatusCode > 0 {
		return pe.StatusCode
	}
	return 500
}

// ErrNoAPIKey is returned when the client didn't provide an API key
// and the gateway has no fallback configured.
var ErrNoAPIKey = errors.New("no API key provided: client must send Authorization: Bearer <key>")

// ProviderError wraps an upstream HTTP error so handlers can read
// the original status code. The Body is the raw upstream response —
// never expose it directly to clients.
type ProviderError struct {
	StatusCode int
	Body       string
	Provider   string
}

// Error implements the error interface. It produces a safe summary
// suitable for LOGS — it does NOT contain the full upstream body.
func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s upstream error (status %d): %s",
		e.Provider, e.StatusCode, truncate(e.Body, maxBodyLogBytes))
}

// ClientMessage returns a short, sanitized message safe to send to clients.
// It never includes the full upstream body or any internal identifiers
// (like request IDs) that the upstream may have embedded.
func (e *ProviderError) ClientMessage() string {
	return fmt.Sprintf("Upstream %s returned status %d",
		strings.ToLower(e.Provider), e.StatusCode)
}

// Unwrap allows errors.Is/As to inspect the inner error if needed.
func (e *ProviderError) Unwrap() error { return nil }

// truncate cuts s to at most n bytes, appending an ellipsis marker
// if it was actually shortened. Operates on bytes (not runes) which
// is fine for log truncation where exact character boundaries are
// not critical.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
