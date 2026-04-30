package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

func TestRequireScope_Allows(t *testing.T) {
	h := auth.RequireScope("Display")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/x", nil).WithContext(
		auth.WithPrincipal(context.Background(), &auth.Principal{Scopes: []string{"Display"}}),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestRequireScope_Denies(t *testing.T) {
	h := auth.RequireScope("Admin")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatalf("handler called without scope")
	}))
	req := httptest.NewRequest("GET", "/x", nil).WithContext(
		auth.WithPrincipal(context.Background(), &auth.Principal{Scopes: []string{"Display"}}),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestRequireScope_NoPrincipal(t *testing.T) {
	h := auth.RequireScope("Display")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatalf("handler called without principal")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
