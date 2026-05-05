package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// withCORS adds permissive CORS headers to every response.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Addon-Token")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withAPIToken wraps all /api/* routes with token authentication when a token is configured.
func withAPIToken(expected string, next http.Handler) http.Handler {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimSpace(r.Header.Get("X-Addon-Token"))
		if !apiTokensEqual(expected, got) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "api_token_required",
				"hint":  "Header X-Addon-Token muss mit -api-token uebereinstimmen.",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func apiTokensEqual(expected, got string) bool {
	if len(expected) == 0 || len(expected) != len(got) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(got)) == 1
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

func fmtErr(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}
