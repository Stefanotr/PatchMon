package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// encodeURIComponent escapes s the way the frontend does: everything but
// letters, digits and -_.!~*'() is percent-encoded.
func encodeURIComponent(s string) string {
	e := strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
	for _, c := range []string{"!", "'", "(", ")", "*"} {
		e = strings.ReplaceAll(e, url.QueryEscape(c), c)
	}
	return e
}

func TestURLParamDecodesPackageNames(t *testing.T) {
	for _, name := range []string{
		"Mise à jour de la sélection disjointe pour Microsoft Defender Antivirus – 2267602 Ko (version 1.459.684.0) – Canal actuel (large) (KB2267602)",
		"7-Zip 24.08 (x64)",
		"libstdc++6",
		"g++",
		"openssl",
		"Microsoft Edge",
		"50% off",          // escaped like Go does: no second decoding
		"a%(b)",            // escaped differently: decoded once only
		"repo/name",        // a slash inside the segment
		"Mozilla.Firefox*", // left as is by encodeURIComponent
	} {
		var got string
		var ok bool
		r := chi.NewRouter()
		r.Get("/packages/{packageId}/hosts", func(_ http.ResponseWriter, r *http.Request) {
			got, ok = urlParam(r, "packageId")
		})
		req := httptest.NewRequest(http.MethodGet, "/packages/"+encodeURIComponent(name)+"/hosts?needsUpdate=true", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !ok || got != name {
			t.Errorf("%q: status %d, urlParam = %q, %v", name, rec.Code, got, ok)
		}
	}
}

func TestURLParamRejectsMalformedEscapes(t *testing.T) {
	r := chi.NewRouter()
	var ok = true
	r.Get("/packages/{packageId}", func(_ http.ResponseWriter, r *http.Request) {
		_, ok = urlParam(r, "packageId")
	})
	// net/http never hands over such a raw path; chi routes on it all the
	// same, so the helper must not trust it.
	req := httptest.NewRequest(http.MethodGet, "/packages/x", nil)
	req.URL.RawPath = "/packages/(%zz)"
	r.ServeHTTP(httptest.NewRecorder(), req)
	if ok {
		t.Fatal("a malformed escape was accepted")
	}
}
