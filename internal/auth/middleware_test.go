package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// newMiddlewareValidator builds a generic OIDC validator backed by a MockOIDC
// fixture. The returned audience is the clientID expected by Issue().
func newMiddlewareValidator(t *testing.T) (mock *auth.MockOIDC, v auth.Validator, audience string) {
	t.Helper()

	m, err := auth.NewMockOIDC()
	require.NoError(t, err, "NewMockOIDC")
	t.Cleanup(m.Close)

	const aud = "kafkito-test"
	val, err := auth.NewOIDCValidator(auth.OIDCConfig{
		IssuerURL:    m.Server.URL,
		Audience:     aud,
		JWKSEndpoint: m.JKU(),
	})
	require.NoError(t, err, "NewOIDCValidator")

	return m, val, aud
}

func TestMiddleware_AllowsAuthorizedRequest_InvokesHandler(t *testing.T) {
	t.Parallel()

	mock, v, aud := newMiddlewareValidator(t)
	tok, err := mock.Issue("u-1", aud, mock.Server.URL, []string{"Display"}, nil)
	require.NoError(t, err, "Issue")

	called := false
	mw := auth.Middleware(v)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called, "handler must be invoked when token is valid")
}

func TestMiddleware_PutsPrincipalIntoRequestContext(t *testing.T) {
	t.Parallel()

	mock, v, aud := newMiddlewareValidator(t)
	tok, err := mock.Issue("u-1", aud, mock.Server.URL, []string{"Display"}, nil)
	require.NoError(t, err, "Issue")

	var (
		gotPrincipal *auth.Principal
		gotOK        bool
	)
	mw := auth.Middleware(v)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPrincipal, gotOK = auth.PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	h.ServeHTTP(rec, req)

	require.True(t, gotOK, "principal must be present in request context")
	require.NotNil(t, gotPrincipal)
	assert.Equal(t, "u-1", gotPrincipal.Subject)
}

func TestMiddleware_RejectsRequest_WhenAuthorizationHeaderMissing(t *testing.T) {
	t.Parallel()

	_, v, _ := newMiddlewareValidator(t)
	called := false
	mw := auth.Middleware(v)
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	h.ServeHTTP(rec, req)

	assert.False(t, called, "handler must not run when Authorization header is missing")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestMiddleware_RejectsRequest_WhenBearerTokenMalformed(t *testing.T) {
	t.Parallel()

	_, v, _ := newMiddlewareValidator(t)
	called := false
	mw := auth.Middleware(v)
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")

	h.ServeHTTP(rec, req)

	assert.False(t, called, "handler must not run when bearer token is malformed")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
