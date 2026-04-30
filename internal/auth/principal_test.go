package auth_test

import (
	"context"
	"testing"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

func TestPrincipalContextRoundtrip(t *testing.T) {
	p := &auth.Principal{
		Subject: "u-123",
		Email:   "user@example.com",
		Scopes:  []string{"Display"},
	}
	ctx := auth.WithPrincipal(context.Background(), p)

	got, ok := auth.PrincipalFromContext(ctx)
	if !ok {
		t.Fatalf("PrincipalFromContext: ok = false, want true")
	}
	if got.Subject != "u-123" {
		t.Errorf("Subject = %q, want %q", got.Subject, "u-123")
	}
	if !got.HasScope("Display") {
		t.Errorf("HasScope(Display) = false, want true")
	}
	if got.HasScope("Admin") {
		t.Errorf("HasScope(Admin) = true, want false")
	}
}

func TestPrincipalFromContext_Empty(t *testing.T) {
	if _, ok := auth.PrincipalFromContext(context.Background()); ok {
		t.Errorf("PrincipalFromContext on empty ctx: ok = true, want false")
	}
}
