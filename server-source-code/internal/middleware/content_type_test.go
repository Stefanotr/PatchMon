package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequireJSON(t *testing.T) {
	reached := false
	h := RequireJSON(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		contentType string
		want        int
	}{
		{"application/json", http.StatusNoContent},
		{"application/json; charset=utf-8", http.StatusNoContent},
		{"Application/JSON", http.StatusNoContent},
		{"", http.StatusUnsupportedMediaType},
		{"text/plain", http.StatusUnsupportedMediaType},
		{"text/plain; charset=utf-8", http.StatusUnsupportedMediaType},
		{"application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
		{"multipart/form-data; boundary=x", http.StatusUnsupportedMediaType},
		{"application/json-patch+json", http.StatusUnsupportedMediaType},
		{"application/json;;", http.StatusUnsupportedMediaType},
	} {
		reached = false
		req := httptest.NewRequest(http.MethodPost, "/hosts/h1/reboot/cancel", strings.NewReader("{}"))
		if tc.contentType != "" {
			req.Header.Set("Content-Type", tc.contentType)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("Content-Type %q: status %d, want %d", tc.contentType, rec.Code, tc.want)
		}
		if reached != (tc.want == http.StatusNoContent) {
			t.Errorf("Content-Type %q: handler reached = %v", tc.contentType, reached)
		}
		if tc.want == http.StatusUnsupportedMediaType && !strings.Contains(rec.Body.String(), "Content-Type must be application/json") {
			t.Errorf("Content-Type %q: body %s", tc.contentType, rec.Body.String())
		}
	}
}
