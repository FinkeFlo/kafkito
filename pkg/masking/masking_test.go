// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package masking

import (
	"testing"

	"github.com/FinkeFlo/kafkito/pkg/config"
)

func TestPolicyApply(t *testing.T) {
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
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if p.IsEmpty() {
		t.Fatal("policy should not be empty")
	}

	tests := []struct {
		topic, in, want string
		masked          bool
	}{
		{
			"orders",
			`{"email":"a@b.c","amount":5}`,
			`{"amount":5,"email":"***"}`,
			true,
		},
		{
			"user-pii",
			`{"customer":{"phone":"+49123","name":"A"}}`,
			`{"customer":{"name":"A","phone":"***"}}`,
			true,
		},
		{
			"notes",
			"card 4111111111111111 expired",
			"card **** expired",
			true,
		},
		{
			"other",
			`{"email":"keep@example.com"}`,
			`{"email":"keep@example.com"}`,
			false,
		},
	}
	for _, tc := range tests {
		got, masked := p.Apply(tc.topic, tc.in)
		if masked != tc.masked || got != tc.want {
			t.Errorf("Apply(%q,%q): got (%q,%v), want (%q,%v)", tc.topic, tc.in, got, masked, tc.want, tc.masked)
		}
	}
}

func TestPolicyCompileError(t *testing.T) {
	if _, err := Compile([]config.MaskingRule{{Topics: []string{"["}}}); err == nil {
		t.Fatal("expected error for invalid topic regex")
	}
}
