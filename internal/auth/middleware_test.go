package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// newMiddlewareValidator builds a generic OIDC validator backed by a MockOIDC
// fixture. The returned audience is the clientID expected by Issue().
func newMiddlewareValidator(t *testing.T) (mock *auth.MockOIDC, v auth.Validator, audience string) {
	t.Helper()
	m, err := auth.NewMockOIDC()
	if err != nil {
		t.Fatalf("mock: %v", err)
	}
	t.Cleanup(m.Close)
	const aud = "kafkito-test"
	val, err := auth.NewOIDCValidator(auth.OIDCConfig{
		IssuerURL:    m.Server.URL,
		Audience:     aud,
		JWKSEndpoint: m.JKU(),
	})
	if err != nil {
		t.Fatalf("NewOIDCValidator: %v", err)
	}
	return m, val, aud
}

func TestMiddleware_Allows(t *testing.T) {
	mock, v, aud := newMiddlewareValidator(t)
	tok, _ := mock.Issue("u-1", aud, mock.Server.URL, []string{"Display"}, nil)

	mw := auth.Middleware(v)
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		p, ok := auth.PrincipalFromContext(r.Context())
		if !ok || p.Subject != "u-1" {
			t.Errorf("principal missing or wrong: %+v", p)
		}
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(rec, req)

	if !called {
		t.Errorf("handler not called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestMiddleware_RejectsMissingHeader(t *testing.T) {
	_, v, _ := newMiddlewareValidator(t)
	mw := auth.Middleware(v)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("handler called for missing token")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_RejectsBadToken(t *testing.T) {
	_, v, _ := newMiddlewareValidator(t)
	mw := auth.Middleware(v)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("handler called for bad token")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
