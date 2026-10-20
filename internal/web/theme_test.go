package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stuttgart-things/homerun2-config-viewer/internal/fixture"
)

const portalTokensLink = `<link rel="stylesheet" href="/static/homerun2-tokens.css">`

// getViaPortal requests target as homerun2-portal forwards it.
func getViaPortal(t *testing.T, mux http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	req.Header.Set("X-Forwarded-Prefix", "/config")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestThePortalTokensAreLinkedOnlyBehindThePortal(t *testing.T) {
	res, err := fixture.Mixup()
	mux := newMuxAt(t, of(t, res, err), "/config")

	for _, path := range []string{"/", "/matrix", "/dryrun"} {
		if body := htmlOf(t, get(t, mux, path), http.StatusOK); strings.Contains(body, portalTokensLink) {
			t.Errorf("%s: the page links the portal's tokens without the portal", path)
		}
		body := htmlOf(t, getViaPortal(t, mux, path), http.StatusOK)
		own := strings.Index(body, `href="/config/static/viewer.css"`)
		if i := strings.Index(body, portalTokensLink); i < 0 || own < 0 || i < own {
			t.Errorf("%s: behind the portal the page does not link its tokens after its own copy", path)
		}
	}
}

func TestThePageSwitchesToTheCompactHeaderInsideThePortal(t *testing.T) {
	head, _, _ := strings.Cut(htmlOf(t, get(t, mixupMux(t), "/"), http.StatusOK), "</head>")
	script := strings.Index(head, "classList.add('hr-embedded')")
	if script < 0 || script > strings.Index(head, `rel="stylesheet"`) {
		t.Error("the hr-embedded script is missing or runs after the styles")
	}

	css := get(t, mixupMux(t), "/static/viewer.css").Body.String()
	contains(t, css, ":where(:root) {", "--hr-surface:", ".hr-embedded .header-bar {")
}
