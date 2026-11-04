// Package testapi starts fake and mock Shortcut API servers for tests.
package testapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Fake starts a server that serves routes, keyed by "METHOD /path".
// It returns the base URL. The server stops in t.Cleanup.
func Fake(t *testing.T, routes map[string]http.HandlerFunc) string {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, h := range routes {
		mux.HandleFunc(pattern, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}
