// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The auth-scheme is case-insensitive (RFC 7235 section 2.1), and whitespace
// around the header value and between scheme and token is not part of the
// token.
func TestBearerToken_ParsesTheScheme(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"canonical", "Bearer abc.def.ghi", "abc.def.ghi"},
		{"lower_case", "bearer abc.def.ghi", "abc.def.ghi"},
		{"upper_case", "BEARER abc.def.ghi", "abc.def.ghi"},
		{"mixed_case", "bEaReR abc.def.ghi", "abc.def.ghi"},
		{"surrounding_whitespace", "  Bearer abc.def.ghi \t", "abc.def.ghi"},
		{"several_spaces", "Bearer    abc.def.ghi", "abc.def.ghi"},
		{"tab_separator", "Bearer\tabc.def.ghi", "abc.def.ghi"},
		{"missing", "", ""},
		{"scheme_only", "Bearer", ""},
		{"scheme_and_space_only", "Bearer   ", ""},
		{"other_scheme", "Basic dXNlcjpwYXNz", ""},
		{"scheme_prefix_without_separator", "Bearerabc.def.ghi", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequest("GET", "/x", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}

			assert.Equal(t, tc.want, bearerToken(r))
		})
	}
}
