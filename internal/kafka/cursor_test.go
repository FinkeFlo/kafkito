// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCursor_RoundTrip(t *testing.T) {
	t.Parallel()
	c := Cursor{
		Direction: CursorBackward,
		Partitions: map[int32]int64{
			0: 1234,
			1: 5678,
			2: 998,
		},
	}
	enc, err := EncodeCursor(c)
	require.NoError(t, err)
	require.NotEmpty(t, enc)

	dec, err := DecodeCursor(enc)
	require.NoError(t, err)
	require.Equal(t, c.Direction, dec.Direction)
	require.Equal(t, c.Partitions, dec.Partitions)
}

func TestCursor_RoundTrip_Forward(t *testing.T) {
	t.Parallel()
	c := Cursor{Direction: CursorForward, Partitions: map[int32]int64{0: 0}}
	enc, err := EncodeCursor(c)
	require.NoError(t, err)
	dec, err := DecodeCursor(enc)
	require.NoError(t, err)
	require.Equal(t, CursorForward, dec.Direction)
	require.Equal(t, c.Partitions, dec.Partitions)
}

func TestCursor_Encode_RejectsBadDirection(t *testing.T) {
	t.Parallel()
	_, err := EncodeCursor(Cursor{Direction: "sideways", Partitions: map[int32]int64{0: 1}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid direction")
}

func TestCursor_Decode_Errors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", "empty cursor"},
		{"not_base64", "!!not-base64!!", "decode base64"},
		{"not_json", base64Std("foobar"), "decode json"},
		{"missing_direction", base64Std(`{"p":{"0":1}}`), "invalid direction"},
		{"bad_direction", base64Std(`{"p":{"0":1},"d":"sideways"}`), "invalid direction"},
		{"missing_partitions", base64Std(`{"d":"backward"}`), "missing partitions"},
		{"bad_partition_key", base64Std(`{"p":{"x":1},"d":"backward"}`), "invalid partition key"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeCursor(tc.raw)
			require.Error(t, err)
			require.Truef(t, strings.Contains(err.Error(), tc.want),
				"err = %q, want substring %q", err.Error(), tc.want)
		})
	}
}

func TestCursor_Decode_AcceptsURLSafeBase64(t *testing.T) {
	t.Parallel()
	c := Cursor{Direction: CursorBackward, Partitions: map[int32]int64{0: 1, 1: 2}}
	enc, err := EncodeCursor(c)
	require.NoError(t, err)
	// EncodeCursor emits std base64; assert URL-safe variant also decodes.
	urlSafe := strings.NewReplacer("+", "-", "/", "_").Replace(enc)
	dec, err := DecodeCursor(urlSafe)
	require.NoError(t, err)
	require.Equal(t, c.Partitions, dec.Partitions)
}

// base64Std encodes a string with std base64 for use in test fixtures.
func base64Std(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
