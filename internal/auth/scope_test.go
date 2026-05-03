package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

func TestRequireScope_EnforcesPolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		requiredScope     string
		principal         *auth.Principal
		wantStatus        int
		wantHandlerCalled bool
	}{
		{
			name:              "allows_when_principal_has_required_scope",
			requiredScope:     "Display",
			principal:         &auth.Principal{Scopes: []string{"Display"}},
			wantStatus:        http.StatusOK,
			wantHandlerCalled: true,
		},
		{
			name:              "denies_when_principal_lacks_required_scope",
			requiredScope:     "Admin",
			principal:         &auth.Principal{Scopes: []string{"Display"}},
			wantStatus:        http.StatusForbidden,
			wantHandlerCalled: false,
		},
		{
			name:              "denies_when_request_has_no_principal",
			requiredScope:     "Display",
			principal:         nil,
			wantStatus:        http.StatusUnauthorized,
			wantHandlerCalled: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			called := false
			h := auth.RequireScope(tc.requiredScope)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tc.principal != nil {
				req = req.WithContext(auth.WithPrincipal(context.Background(), tc.principal))
			}
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, tc.wantHandlerCalled, called,
				"handler invocation expectation; required=%q principal=%+v", tc.requiredScope, tc.principal)
		})
	}
}
