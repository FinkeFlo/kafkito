// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package netguard_test

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/netguard"
)

func TestValidateHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host    string
		wantErr bool
	}{
		{"127.0.0.1", true},               // loopback literal
		{"127.0.0.1:9092", true},          // host:port form is split
		{"[::1]:9092", true},              // bracketed v6 literal
		{"[::1]", true},                   // bracketed v6 literal without port
		{"169.254.169.254:80", true},      // cloud metadata
		{"0.0.0.0:9092", true},            // unspecified
		{"localhost:9092", true},          // hostname resolving to loopback
		{"", true},                        // empty
		{"   ", true},                     // blank
		{":9092", true},                   // port only
		{"203.0.113.10:9092", false},      // public literal
		{" 10.0.0.5:9092", false},         // surrounding whitespace trimmed
		{"192.168.1.10", false},           // RFC-1918 allowed
		{"host.invalid:9092", true},       // unresolvable (RFC 6761 .invalid)
		{"::ffff:127.0.0.1", true},        // v4-mapped loopback
		{"[::ffff:10.0.0.5]:9092", false}, // v4-mapped private
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			err := netguard.ValidateHost(tc.host)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestValidateURL(t *testing.T) {
	t.Parallel()

	require.Error(t, netguard.ValidateURL("http://127.0.0.1:8081"), "loopback must be blocked")
	require.Error(t, netguard.ValidateURL("http://169.254.169.254/latest/meta-data/"), "metadata must be blocked")
	require.Error(t, netguard.ValidateURL("ftp://example.com"), "non-http(s) scheme must be blocked")
	require.Error(t, netguard.ValidateURL("://nonsense"), "unparseable url must be blocked")
	require.Error(t, netguard.ValidateURL("http:///path"), "url without host must be blocked")
	assert.NoError(t, netguard.ValidateURL("https://203.0.113.10:8081"), "public host must pass")
	assert.NoError(t, netguard.ValidateURL("HTTPS://203.0.113.10"), "scheme is case-insensitive")
}

// resolverServer is the address a fake lookup names in its errors, like the
// system resolver does. It must never reach an error text.
const resolverServer = "192.0.2.53:53"

func fakeLookup(_ context.Context, host string) ([]string, error) {
	switch host {
	case "loopback.test":
		return []string{"10.0.0.1", "127.0.0.1"}, nil
	case "public.test":
		return []string{"203.0.113.10"}, nil
	case "empty.test":
		return nil, nil
	default:
		return nil, &net.DNSError{Err: "no such host", Name: host, Server: resolverServer, IsNotFound: true}
	}
}

// The errors are the fixed sentinels: no host, resolved address or
// resolver detail, so a caller may hand them to the client.
func TestHostValidator_Host_FixedErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want error
	}{
		{"", netguard.ErrEmptyHost},
		{" :9092", netguard.ErrEmptyHost},
		{"127.0.0.1:9092", netguard.ErrHostNotAllowed},
		{"[::1]", netguard.ErrHostNotAllowed},
		{"169.254.169.254", netguard.ErrHostNotAllowed},
		{"loopback.test:9092", netguard.ErrHostNotAllowed},
		{"missing.test:9092", netguard.ErrUnresolvable},
		{"empty.test:9092", netguard.ErrUnresolvable},
		{"public.test:9092", nil},
		{"203.0.113.10:9092", nil},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			err := netguard.NewHostValidator(fakeLookup).Host(context.Background(), tc.host)
			if tc.want == nil {
				assert.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
			assert.Equal(t, tc.want.Error(), err.Error(), "the text is the sentinel's own")
		})
	}
}

func TestHostValidator_URL_FixedErrors(t *testing.T) {
	t.Parallel()

	const password = "Url-Password-Canary"
	cases := []struct {
		name, raw string
		want      error
	}{
		{"space in host", "http://user:" + password + "@bad host:8081", netguard.ErrInvalidURL},
		{"unclosed bracket", "http://user:" + password + "@[::1", netguard.ErrInvalidURL},
		{"scheme", "ftp://user:" + password + "@public.test", netguard.ErrURLScheme},
		{"no host", "http://user:" + password + "@/path", netguard.ErrURLNoHost},
		{"loopback literal", "http://user:" + password + "@127.0.0.1:8081", netguard.ErrHostNotAllowed},
		{"bracketed loopback", "http://user:" + password + "@[::1]", netguard.ErrHostNotAllowed},
		{"unresolvable", "https://user:" + password + "@missing.test", netguard.ErrUnresolvable},
		{"allowed", "https://user:" + password + "@public.test:8081", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := netguard.NewHostValidator(fakeLookup).URL(context.Background(), tc.raw)
			if tc.want == nil {
				assert.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
			assert.Equal(t, tc.want.Error(), err.Error(), "the text is the sentinel's own")
			assert.NotContains(t, err.Error(), password)
		})
	}
}

type ctxKey struct{}

// A definition that repeats a host costs one lookup per distinct host, in
// any letter case, for brokers and the Schema Registry URL alike. Failed
// lookups are kept too, and the request context reaches the resolver.
func TestHostValidator_ResolvesEachHostOnce(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(context.Background(), ctxKey{}, "request")
	calls := map[string]int{}
	lookup := func(ctx context.Context, host string) ([]string, error) {
		assert.Equal(t, "request", ctx.Value(ctxKey{}), "the lookup runs under the caller's context")
		calls[host]++
		return fakeLookup(ctx, host)
	}
	v := netguard.NewHostValidator(lookup)
	for _, h := range []string{"public.test:9092", "public.test:9093", "PUBLIC.test:9094", " public.test:9095", "203.0.113.20:9092"} {
		require.NoError(t, v.Host(ctx, h))
	}
	require.NoError(t, v.URL(ctx, "https://public.test:8081"))
	for range 3 {
		require.ErrorIs(t, v.Host(ctx, "missing.test:9092"), netguard.ErrUnresolvable)
	}
	assert.Equal(t, map[string]int{"public.test": 1, "missing.test": 1}, calls)
}

func TestNewHostValidator_DefaultResolver(t *testing.T) {
	t.Parallel()

	v := netguard.NewHostValidator(nil)
	require.ErrorIs(t, v.Host(context.Background(), "localhost:9092"), netguard.ErrHostNotAllowed)
	require.ErrorIs(t, v.Host(context.Background(), "host.invalid:9092"), netguard.ErrUnresolvable)
}

// A resolver that answers without an address and without an error leaves
// nothing to dial; the guarded dialer reports that as unresolvable.
func TestGuardedDialWith_NoAddressesIsUnresolvable(t *testing.T) {
	t.Parallel()

	dial := netguard.GuardedDialWith(
		func(context.Context, string, string) ([]netip.Addr, error) { return nil, nil },
		func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("nothing to dial")
			return nil, nil
		},
	)
	_, err := dial(context.Background(), "tcp", "empty.test:9092")
	require.ErrorIs(t, err, netguard.ErrUnresolvable)
}
