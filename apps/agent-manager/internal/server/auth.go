package server

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func bearerAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			provided, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == "" || !ok || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeAPIError(w, http.StatusUnauthorized, "unauthorized", "a valid bearer token is required", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
