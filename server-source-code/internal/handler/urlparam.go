package handler

import (
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
)

// urlParam returns the route parameter key, decoded once.
//
// chi matches routes on the escaped path (r.URL.RawPath) whenever the client
// escaped it differently from Go, as encodeURIComponent does for "+", "(",
// ")" and "/". Its parameters are then still escaped: a package name such as
// "Security Update (KB2267602)" or "libstdc++6" would be looked up as
// "Security%20Update%20(KB2267602)" and never found. Otherwise chi matched
// the decoded path and the parameter must not be decoded a second time.
// ok is false for a malformed escape.
func urlParam(r *http.Request, key string) (value string, ok bool) {
	v := chi.URLParam(r, key)
	if r.URL.RawPath == "" {
		return v, true
	}
	u, err := url.PathUnescape(v)
	if err != nil {
		return "", false
	}
	return u, true
}
