// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectContentType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		in       []byte
		wantMIME string
		wantExt  string
	}{
		{"empty", []byte{}, "application/octet-stream", "bin"},
		{"binary", []byte{0xff, 0xfe, 0x00, 0x01}, "application/octet-stream", "bin"},
		{"json_object", []byte(`{"a":1}`), "application/json", "json"},
		{"json_array", []byte(`[1]`), "application/json", "json"},
		{"invalid_json", []byte(`{`), "text/plain; charset=utf-8", "txt"},
		{"plain_text", []byte("hello"), "text/plain; charset=utf-8", "txt"},
		{"xml_element", []byte(`<a>1</a>`), "application/xml", "xml"},
		{"xml_declaration", []byte(`<?xml version="1.0"?><a/>`), "application/xml", "xml"},
		{"xml_leading_whitespace", []byte("\n  <a>1</a>\n"), "application/xml", "xml"},
		{"xml_unclosed", []byte(`<a>`), "text/plain; charset=utf-8", "txt"},
		{"angle_bracket_text", []byte(`<3 kafka`), "text/plain; charset=utf-8", "txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotMIME, gotExt := detectContentType(tc.in)

			assert.Equal(t, tc.wantMIME, gotMIME)
			assert.Equal(t, tc.wantExt, gotExt)
		})
	}
}
