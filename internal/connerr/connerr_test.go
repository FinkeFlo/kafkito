// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package connerr_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/netip"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl/plain"

	"github.com/FinkeFlo/kafkito/internal/connerr"
	"github.com/FinkeFlo/kafkito/internal/netguard"
)

// Details the classified errors carry. None of them may reach a message.
const (
	brokerIP     = "198.51.100.7"
	brokerHost   = "broker-canary.example"
	resolverAddr = "192.0.2.53:53"
	brokerText   = "Broker-Message-Canary"
)

// dialErr wraps err the way franz-go and the guarded dialer wrap a failed
// dial, with the host and IP in the text.
func dialErr(err error) error {
	op := &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP(brokerIP), Port: 9092}, Err: err}
	return fmt.Errorf("unable to dial: dial %s: all addresses failed: %w", brokerHost, op)
}

func TestClassify(t *testing.T) {
	t.Parallel()

	certErr := &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}
	hostnameErr := &tls.CertificateVerificationError{
		Err: x509.HostnameError{Certificate: &x509.Certificate{}, Host: brokerHost},
	}
	cases := []struct {
		name string
		err  error
		want connerr.Class
	}{
		{"refused", dialErr(&os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}), connerr.Refused},
		{"timeout: dial deadline", dialErr(os.ErrDeadlineExceeded), connerr.Timeout},
		{"timeout: context", fmt.Errorf("broker list: %w", context.DeadlineExceeded), connerr.Timeout},
		{"timeout: ETIMEDOUT", dialErr(&os.SyscallError{Syscall: "connect", Err: syscall.ETIMEDOUT}), connerr.Timeout},
		{"dns: not found", fmt.Errorf("unable to dial: %w",
			&net.DNSError{Err: "no such host", Name: brokerHost, Server: resolverAddr, IsNotFound: true}), connerr.DNS},
		{"dns: resolver timeout", fmt.Errorf("unable to dial: %w",
			&net.DNSError{Err: "i/o timeout", Name: brokerHost, Server: resolverAddr, IsTimeout: true}), connerr.DNS},
		{"dns: validation", netguard.ErrUnresolvable, connerr.DNS},
		{"dns: guarded dial without addresses", fmt.Errorf("dial %s: %w", brokerHost, netguard.ErrUnresolvable), connerr.DNS},
		{"tls: not a TLS peer", fmt.Errorf("unable to dial: %w",
			tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}), connerr.TLS},
		{"tls: unknown authority", fmt.Errorf("unable to dial: %w", certErr), connerr.TLS},
		{"tls: host name mismatch", fmt.Errorf("unable to dial: %w", hostnameErr), connerr.TLS},
		{"tls: alert from the peer", fmt.Errorf("unable to dial: %w",
			&net.OpError{Op: "remote error", Err: errors.New("tls: handshake failure")}), connerr.TLS},
		{"tls: broker closed after dial", &kgo.ErrFirstReadEOF{}, connerr.TLS},
		{"tls: marked handshake EOF", fmt.Errorf("unable to dial: %w",
			connerr.WithClass(connerr.TLS, fmt.Errorf("tls handshake to %s: %w", brokerHost, io.EOF))), connerr.TLS},
		{"tls: marked handshake timeout", fmt.Errorf("unable to dial: %w",
			connerr.WithClass(connerr.TLS, fmt.Errorf("tls handshake to %s: %w", brokerHost, context.DeadlineExceeded))), connerr.TLS},
		{"sasl: authentication failed", fmt.Errorf("%s: %w", brokerText, kerr.SaslAuthenticationFailed), connerr.SASL},
		{"sasl: unsupported mechanism", kerr.UnsupportedSaslMechanism, connerr.SASL},
		{"sasl: illegal state", kerr.IllegalSaslState, connerr.SASL},
		{"blocked: dial", fmt.Errorf("unable to dial: %w", &netguard.BlockedAddressError{
			Addr: brokerHost + ":9092", Host: brokerHost, IP: netip.MustParseAddr("127.0.0.1"),
		}), connerr.Blocked},
		{"blocked: validation", netguard.ErrHostNotAllowed, connerr.Blocked},
		{"fallback: reset", dialErr(&os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}), connerr.Unreachable},
		{"fallback: EOF", io.EOF, connerr.Unreachable},
		{"fallback: client closed", kgo.ErrClientClosed, connerr.Unreachable},
		{"fallback: other text", errors.New(brokerText + " at " + brokerIP), connerr.Unreachable},
		{"marker wins", connerr.WithClass(connerr.SASL, dialErr(syscall.ECONNREFUSED)), connerr.SASL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, connerr.Classify(tc.err))
			msg := connerr.Message(tc.err)
			assert.Equal(t, tc.want.Message(), msg)
			for _, detail := range []string{brokerIP, brokerHost, resolverAddr, brokerText, "dial", "connect:", "tls:", "EOF"} {
				assert.NotContains(t, msg, detail)
			}
		})
	}
}

func TestClassify_Nil(t *testing.T) {
	t.Parallel()

	assert.Equal(t, connerr.Class(""), connerr.Classify(nil))
	assert.Empty(t, connerr.Message(nil))
	assert.NoError(t, connerr.WithClass(connerr.TLS, nil))
}

func TestClassMessages(t *testing.T) {
	t.Parallel()

	want := map[connerr.Class]string{
		connerr.Refused:     "connection refused",
		connerr.Timeout:     "connection timed out",
		connerr.DNS:         "host name could not be resolved",
		connerr.TLS:         "TLS handshake failed",
		connerr.SASL:        "authentication failed",
		connerr.Blocked:     "destination not allowed",
		connerr.Unreachable: "broker not reachable",
	}
	got := map[connerr.Class]string{}
	for _, c := range connerr.Classes() {
		got[c] = c.Message()
	}
	assert.Equal(t, want, got)
	assert.Empty(t, connerr.Class("other").Message())
}

// The validation sentinels of netguard carry the texts of their classes, so
// a validation message and a connection message read the same.
func TestClassMessages_MatchNetguardSentinels(t *testing.T) {
	t.Parallel()

	assert.Equal(t, connerr.DNS.Message(), netguard.ErrUnresolvable.Error())
	assert.Equal(t, connerr.Blocked.Message(), netguard.ErrHostNotAllowed.Error())
}

func TestWithClass_KeepsTextAndChain(t *testing.T) {
	t.Parallel()

	err := connerr.WithClass(connerr.TLS, fmt.Errorf("tls handshake to %s: %w", brokerHost, io.EOF))
	assert.Equal(t, "tls handshake to "+brokerHost+": EOF", err.Error())
	require.ErrorIs(t, err, io.EOF)
}

// Errors as the standard library returns them.
func TestClassify_RealNetworkErrors(t *testing.T) {
	t.Parallel()

	t.Run("refused", func(t *testing.T) {
		t.Parallel()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := ln.Addr().String()
		require.NoError(t, ln.Close())
		_, err = net.Dial("tcp", addr)
		require.Error(t, err)
		assert.Equal(t, connerr.Refused, connerr.Classify(err), err.Error())
	})
	t.Run("timeout", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancel()
		var d net.Dialer
		_, err := d.DialContext(ctx, "tcp", "192.0.2.1:9092")
		require.Error(t, err)
		assert.Equal(t, connerr.Timeout, connerr.Classify(err), err.Error())
	})
	t.Run("dns", func(t *testing.T) {
		t.Parallel()
		_, err := net.DefaultResolver.LookupHost(t.Context(), "broker.invalid")
		require.Error(t, err)
		assert.Equal(t, connerr.DNS, connerr.Classify(err), err.Error())
	})
	t.Run("tls: untrusted certificate", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewTLSServer(nil)
		defer srv.Close()
		_, err := tls.Dial("tcp", srv.Listener.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS12})
		require.Error(t, err)
		assert.Equal(t, connerr.TLS, connerr.Classify(err), err.Error())
	})
	t.Run("tls: plaintext peer", func(t *testing.T) {
		t.Parallel()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			_, _ = conn.Write([]byte("HTTP/1.0 400 Bad Request\r\n\r\n"))
			_ = conn.Close()
		}()
		_, err = tls.Dial("tcp", ln.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS12})
		require.Error(t, err)
		assert.Equal(t, connerr.TLS, connerr.Classify(err), err.Error())
	})
}

func ping(t *testing.T, opts ...kgo.Opt) error {
	t.Helper()
	cl, err := kgo.NewClient(opts...)
	require.NoError(t, err)
	defer cl.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	return cl.Ping(ctx)
}

// Errors as franz-go returns them from a broker.
func TestClassify_RealBrokerErrors(t *testing.T) {
	t.Parallel()

	newCluster := func(t *testing.T, opts ...kfake.Opt) *kfake.Cluster {
		t.Helper()
		c, err := kfake.NewCluster(append([]kfake.Opt{kfake.NumBrokers(1)}, opts...)...)
		require.NoError(t, err)
		t.Cleanup(c.Close)
		return c
	}
	saslOpts := []kfake.Opt{kfake.EnableSASL(), kfake.Superuser("PLAIN", "admin", "right")}

	t.Run("sasl: required but not configured", func(t *testing.T) {
		t.Parallel()
		c := newCluster(t, saslOpts...)
		err := ping(t, kgo.SeedBrokers(c.ListenAddrs()...))
		require.Error(t, err)
		assert.Equal(t, connerr.SASL, connerr.Classify(err), err.Error())
	})
	t.Run("sasl: credentials refused", func(t *testing.T) {
		t.Parallel()
		c := newCluster(t, saslOpts...)
		// Like a real broker, answer with SASL_AUTHENTICATION_FAILED instead
		// of closing the connection as kfake does.
		c.ControlKey(int16(kmsg.SASLAuthenticate), func(req kmsg.Request) (kmsg.Response, error, bool) {
			c.KeepControl()
			resp := req.(*kmsg.SASLAuthenticateRequest).ResponseKind().(*kmsg.SASLAuthenticateResponse)
			resp.ErrorCode = kerr.SaslAuthenticationFailed.Code
			msg := brokerText
			resp.ErrorMessage = &msg
			return resp, nil, true
		})
		err := ping(t, kgo.SeedBrokers(c.ListenAddrs()...), kgo.SASL(plain.Auth{User: "admin", Pass: "wrong"}.AsMechanism()))
		require.Error(t, err)
		assert.Equal(t, connerr.SASL, connerr.Classify(err), err.Error())
	})
	t.Run("tls: broker requires TLS", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewTLSServer(nil)
		defer srv.Close()
		c := newCluster(t, kfake.TLS(srv.TLS.Clone()))
		err := ping(t, kgo.SeedBrokers(c.ListenAddrs()...))
		require.Error(t, err)
		assert.Equal(t, connerr.TLS, connerr.Classify(err), err.Error())
	})
	t.Run("tls: untrusted broker certificate", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewTLSServer(nil)
		defer srv.Close()
		c := newCluster(t, kfake.TLS(srv.TLS.Clone()))
		err := ping(t, kgo.SeedBrokers(c.ListenAddrs()...), kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
		require.Error(t, err)
		assert.Equal(t, connerr.TLS, connerr.Classify(err), err.Error())
	})
}
