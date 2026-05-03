// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package masking

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
)

func newApplyPolicy(t *testing.T) *Policy {
	t.Helper()

	p, err := Compile([]config.MaskingRule{
		{
			Topics: []string{"orders", "user-.*"},
			Fields: []string{"$.email", "$.customer.phone"},
		},
		{
			Regex: []config.RegexMask{
				{Match: `\b\d{16}\b`, Replacement: "****"},
			},
		},
	})
	require.NoError(t, err, "Compile fixture policy")
	return p
}

func TestPolicy_IsEmpty_FalseWhenRulesPresent(t *testing.T) {
	t.Parallel()

	p := newApplyPolicy(t)

	assert.False(t, p.IsEmpty(), "policy with field + regex rules must not report empty")
}

func TestPolicyApply_MasksFieldsAndPatterns(t *testing.T) {
	t.Parallel()

	p := newApplyPolicy(t)

	cases := []struct {
		name       string
		topic      string
		in         string
		want       string
		wantMasked bool
	}{
		{
			name:       "orders_field_mask",
			topic:      "orders",
			in:         `{"email":"a@b.c","amount":5}`,
			want:       `{"amount":5,"email":"***"}`,
			wantMasked: true,
		},
		{
			name:       "user_pii_field_mask",
			topic:      "user-pii",
			in:         `{"customer":{"phone":"+49123","name":"A"}}`,
			want:       `{"customer":{"name":"A","phone":"***"}}`,
			wantMasked: true,
		},
		{
			name:       "regex_card_mask",
			topic:      "notes",
			in:         "card 4111111111111111 expired",
			want:       "card **** expired",
			wantMasked: true,
		},
		{
			name:       "no_match_passes_through",
			topic:      "other",
			in:         `{"email":"keep@example.com"}`,
			want:       `{"email":"keep@example.com"}`,
			wantMasked: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, masked := p.Apply(tc.topic, tc.in)

			assert.Equal(t, tc.want, got, "rendered output for topic=%q", tc.topic)
			assert.Equal(t, tc.wantMasked, masked, "masked flag for topic=%q", tc.topic)
		})
	}
}

func TestPolicyCompile_RejectsInvalidTopicRegex(t *testing.T) {
	t.Parallel()

	_, err := Compile([]config.MaskingRule{{Topics: []string{"["}}})

	assert.Error(t, err, "Compile must reject malformed topic regex")
}
