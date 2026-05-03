package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

func TestPrincipal_RoundTripsThroughContext(t *testing.T) {
	t.Parallel()

	p := &auth.Principal{
		Subject: "u-123",
		Email:   "user@example.com",
		Scopes:  []string{"Display"},
	}
	ctx := auth.WithPrincipal(context.Background(), p)

	got, ok := auth.PrincipalFromContext(ctx)

	require.True(t, ok, "PrincipalFromContext must report ok=true after WithPrincipal")
	require.NotNil(t, got)
	assert.Equal(t, "u-123", got.Subject)
	assert.True(t, got.HasScope("Display"), "HasScope(Display) must be true")
	assert.False(t, got.HasScope("Admin"), "HasScope(Admin) must be false")
}

func TestPrincipal_FromContextReportsAbsenceWhenEmpty(t *testing.T) {
	t.Parallel()

	_, ok := auth.PrincipalFromContext(context.Background())

	assert.False(t, ok, "PrincipalFromContext on empty ctx must report ok=false")
}
