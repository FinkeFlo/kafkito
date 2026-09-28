// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package masking

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
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

func TestPolicy_AppliesTo(t *testing.T) {
	t.Parallel()

	scoped, err := Compile([]config.MaskingRule{{Topics: []string{"^orders$"}, Fields: []string{"$.email"}}})
	require.NoError(t, err)
	assert.True(t, scoped.AppliesTo("orders"))
	assert.False(t, scoped.AppliesTo("payments"), "no rule targets the topic")

	assert.True(t, newApplyPolicy(t).AppliesTo("anything"), "a rule without topics applies everywhere")

	empty, err := Compile(nil)
	require.NoError(t, err)
	assert.False(t, empty.AppliesTo("orders"))
	var nilPolicy *Policy
	assert.False(t, nilPolicy.AppliesTo("orders"))
}

func TestPolicy_TargetsDefaultToValue(t *testing.T) {
	t.Parallel()

	p, err := Compile([]config.MaskingRule{{Regex: []config.RegexMask{{Match: `secret`}}}})
	require.NoError(t, err)

	got, masked := p.Apply("t", "a secret")
	assert.True(t, masked)
	assert.Equal(t, "a ***", got)

	got, masked = p.ApplyKey("t", "a secret")
	assert.False(t, masked, "a rule without targets must not mask keys")
	assert.Equal(t, "a secret", got)

	got, masked = p.ApplyHeader("t", "auth", "a secret")
	assert.False(t, masked, "a rule without targets must not mask headers")
	assert.Equal(t, "a secret", got)
}

func TestPolicy_KeyAndHeaderTargets(t *testing.T) {
	t.Parallel()

	p, err := Compile([]config.MaskingRule{
		{
			Topics:  []string{"^orders$"},
			Targets: []string{config.MaskTargetKey},
			Fields:  []string{"$.customer"},
			Regex:   []config.RegexMask{{Match: `C-\d+`, Replacement: "C-***"}},
		},
		{
			Topics:  []string{"^orders$"},
			Targets: []string{config.MaskTargetHeaders},
			Headers: []string{"(?i)^authorization$", "^x-token"},
			Regex:   []config.RegexMask{{Match: `.+`, Replacement: "[redacted]"}},
		},
	})
	require.NoError(t, err)

	cases := []struct {
		name       string
		got        func() (string, bool)
		want       string
		wantMasked bool
	}{
		{"key regex", func() (string, bool) { return p.ApplyKey("orders", "C-1001") }, "C-***", true},
		{"key jsonpath", func() (string, bool) { return p.ApplyKey("orders", `{"customer":"alice","region":"eu"}`) }, `{"customer":"***","region":"eu"}`, true},
		{"key other topic", func() (string, bool) { return p.ApplyKey("payments", "C-1001") }, "C-1001", false},
		{"key rule leaves value", func() (string, bool) { return p.Apply("orders", "C-1001") }, "C-1001", false},
		{"selected header", func() (string, bool) { return p.ApplyHeader("orders", "Authorization", "Bearer abc") }, "[redacted]", true},
		{"selected header prefix", func() (string, bool) { return p.ApplyHeader("orders", "x-token-id", "abc") }, "[redacted]", true},
		{"unselected header", func() (string, bool) { return p.ApplyHeader("orders", "trace-id", "abc") }, "abc", false},
		{"header rule leaves key", func() (string, bool) { return p.ApplyKey("orders", "Bearer abc") }, "Bearer abc", false},
		{"empty header value", func() (string, bool) { return p.ApplyHeader("orders", "Authorization", "") }, "", false},
	}
	for _, tc := range cases {
		got, masked := tc.got()
		assert.Equal(t, tc.want, got, tc.name)
		assert.Equal(t, tc.wantMasked, masked, tc.name)
	}
	assert.True(t, p.AppliesTo("orders"), "key/header rules make the topic masked")
	assert.False(t, p.AppliesTo("payments"))
}

func TestPolicy_HeadersTargetWithoutSelectorMasksEveryHeader(t *testing.T) {
	t.Parallel()

	p, err := Compile([]config.MaskingRule{{
		Targets: []string{config.MaskTargetValue, config.MaskTargetKey, config.MaskTargetHeaders},
		Fields:  []string{"$.pan"},
	}})
	require.NoError(t, err)

	for _, name := range []string{"a", "b", "Authorization"} {
		got, masked := p.ApplyHeader("t", name, `{"pan":"4111"}`)
		assert.True(t, masked, name)
		assert.JSONEq(t, `{"pan":"***"}`, got, name)
	}
	got, masked := p.ApplyHeader("t", "a", "not json")
	assert.False(t, masked, "fields only apply to JSON header values")
	assert.Equal(t, "not json", got)

	_, masked = p.Apply("t", `{"pan":"4111"}`)
	assert.True(t, masked)
	_, masked = p.ApplyKey("t", `{"pan":"4111"}`)
	assert.True(t, masked)
}

func TestPolicyCompile_RejectsInvalidRules(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		rule    config.MaskingRule
		wantErr string
	}{
		"unknown target":         {config.MaskingRule{Targets: []string{"body"}}, `unknown target "body"`},
		"headers without target": {config.MaskingRule{Headers: []string{"auth"}}, `targets does not include "headers"`},
		"bad header pattern":     {config.MaskingRule{Targets: []string{"headers"}, Headers: []string{"("}}, `headers: pattern "("`},
		"bad regex":              {config.MaskingRule{Regex: []config.RegexMask{{Match: "["}}}, `regex: match "["`},
		"bad jsonpath":           {config.MaskingRule{Fields: []string{"$[?("}}, `fields: jsonpath`},
	}
	for name, tc := range cases {
		_, err := Compile([]config.MaskingRule{tc.rule})
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.wantErr, name)
	}
}
