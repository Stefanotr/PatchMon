package middleware

import (
	"mime"
	"net/http"
)

// RequireJSON refuses a request whose body is not declared as JSON (415).
//
// A cross-site form or a simple fetch can only send text/plain, form or
// multipart bodies without a CORS preflight. Requiring application/json
// therefore keeps another site from firing a state-changing request with
// the user's cookies, on top of SameSite, even for routes whose body is
// optional or ignored.
func RequireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeJSONError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		next.ServeHTTP(w, r)
	})
}
