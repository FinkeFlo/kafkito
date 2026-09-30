// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

// Package connerr sorts errors from connecting to a broker or a Schema
// Registry into a few stable classes with fixed texts. A class text never
// contains an address, host name, port, resolver detail or operating system
// error string, so it may be returned to a client that supplied the
// destination. The full error belongs in the server log only.
package connerr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"strings"
	"syscall"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/netguard"
)

// Class is the kind of a connection failure. Its value is stable and
// machine-readable; Message is its client-facing text.
type Class string

// The connection error classes.
const (
	// Refused: the destination refused the connection.
	Refused Class = "refused"
	// Timeout: connecting, or a request on the connection, timed out.
	Timeout Class = "timeout"
	// DNS: a host name did not resolve.
	DNS Class = "dns"
	// TLS: the TLS handshake failed, or one side expected TLS and the
	// other did not.
	TLS Class = "tls"
	// SASL: the broker refused the credentials or the SASL mechanism.
	SASL Class = "sasl"
	// Blocked: the outbound address guard refused the destination.
	Blocked Class = "blocked"
	// Unreachable: any other failure.
	Unreachable Class = "unreachable"
)

// Classes returns every class.
func Classes() []Class {
	return []Class{Refused, Timeout, DNS, TLS, SASL, Blocked, Unreachable}
}

// Message returns the fixed English text of c, or "" when c is not a class.
func (c Class) Message() string {
	switch c {
	case Refused:
		return "connection refused"
	case Timeout:
		return "connection timed out"
	case DNS:
		return "host name could not be resolved"
	case TLS:
		return "TLS handshake failed"
	case SASL:
		return "authentication failed"
	case Blocked:
		return "destination not allowed"
	case Unreachable:
		return "broker not reachable"
	default:
		return ""
	}
}

// Message returns the text of the class of err, or "" when err is nil.
func Message(err error) string {
	return Classify(err).Message()
}

// Classify returns the class of err, or "" when err is nil. An error marked
// by WithClass has the marked class; an error it does not recognise is
// Unreachable.
func Classify(err error) Class {
	if err == nil {
		return ""
	}
	if ce, ok := errors.AsType[*classError](err); ok {
		return ce.class
	}
	switch {
	case errors.Is(err, netguard.ErrBlockedAddress), errors.Is(err, netguard.ErrHostNotAllowed):
		return Blocked
	case isDNS(err):
		return DNS
	case errors.Is(err, syscall.ECONNREFUSED):
		return Refused
	}
	if c, ok := firstReadEOF(err); ok {
		return c
	}
	switch {
	case isTLS(err):
		return TLS
	case isSASL(err):
		return SASL
	case isTimeout(err):
		return Timeout
	default:
		return Unreachable
	}
}

// WithClass marks err as class c for Classify. The result has the text of
// err and unwraps to it. It returns nil when err is nil.
func WithClass(c Class, err error) error {
	if err == nil {
		return nil
	}
	return &classError{class: c, err: err}
}

type classError struct {
	class Class
	err   error
}

func (e *classError) Error() string { return e.err.Error() }

func (e *classError) Unwrap() error { return e.err }

func isDNS(err error) bool {
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return true
	}
	return errors.Is(err, netguard.ErrUnresolvable)
}

// firstReadEOF classifies franz-go's guess for a connection the broker
// closed right away: TLS on one side only, or SASL missing. Only the guess
// for SASL names SASL in its fixed text.
func firstReadEOF(err error) (Class, bool) {
	e, ok := errors.AsType[*kgo.ErrFirstReadEOF](err)
	if !ok {
		return "", false
	}
	if strings.Contains(e.Error(), "SASL") {
		return SASL, true
	}
	return TLS, true
}

func isTLS(err error) bool {
	if _, ok := errors.AsType[tls.RecordHeaderError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return true
	}
	if _, ok := errors.AsType[tls.AlertError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.HostnameError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.CertificateInvalidError](err); ok {
		return true
	}
	// crypto/tls reports most handshake failures, including alerts from the
	// peer, as unexported errors whose text starts with "tls: ". Only the
	// innermost error is matched: the outer texts may contain host names.
	return strings.HasPrefix(innermost(err).Error(), "tls: ")
}

func isSASL(err error) bool {
	return errors.Is(err, kerr.SaslAuthenticationFailed) ||
		errors.Is(err, kerr.UnsupportedSaslMechanism) ||
		errors.Is(err, kerr.IllegalSaslState)
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) ||
		errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	ne, ok := errors.AsType[net.Error](err)
	return ok && ne.Timeout()
}

// innermost follows the single-error Unwrap chain of err to its end.
func innermost(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}
