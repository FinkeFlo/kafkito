// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// clockSkew is the leeway for exp, nbf and iat.
const clockSkew = 60 * time.Second

// allowedAlgorithms are the only JWS algorithms a token may be signed with.
// HMAC algorithms and "none" are never accepted: the verification keys come
// from a public JWKS.
var allowedAlgorithms = map[string]struct{}{
	jwa.RS256().String(): {}, jwa.RS384().String(): {}, jwa.RS512().String(): {},
	jwa.PS256().String(): {}, jwa.PS384().String(): {}, jwa.PS512().String(): {},
	jwa.ES256().String(): {}, jwa.ES384().String(): {}, jwa.ES512().String(): {},
	jwa.EdDSA().String(): {},
}

// Token check errors. They name the rule, never a claim or header value.
var (
	errTokenNoSignature = errors.New("token has no signatures")
	errTokenSignatures  = errors.New("token must carry exactly one signature")
	errAlgNotAllowed    = errors.New("token alg not allowed")
	errAlgKeyMismatch   = errors.New("token alg does not match the key alg")
	errSubMissing       = errors.New("sub claim missing")
	errKidMissing       = errors.New("token has no kid")
	errNestedToken      = errors.New("token payload is not a JSON object")
)

// ParseToken verifies raw against set and validates its time claims. raw
// must be a compact JWS with one signature, a kid and a JSON claim set (no
// nested token). The header alg must be one of the allowed asymmetric algorithms and, when the
// key named by kid carries an alg, equal it; a key without alg is used with
// the algorithm inferred from its type. exp is required, nbf and iat are
// checked when present (60 s skew), and sub must be a non-empty string.
// Issuer and audience are left to the caller.
func ParseToken(raw string, set jwk.Set) (jwt.Token, error) {
	msg, err := jws.Parse([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("parse jws: %w", err)
	}
	switch len(msg.Signatures()) {
	case 0:
		return nil, errTokenNoSignature
	case 1:
	default:
		return nil, errTokenSignatures
	}
	if err := checkAlgorithm(set, msg.Signatures()[0].ProtectedHeaders()); err != nil {
		return nil, err
	}
	// jwt.Parse would verify a nested JWS payload as well, and that inner
	// signature would bypass checkAlgorithm; accept claim sets only.
	if payload := bytes.TrimSpace(msg.Payload()); len(payload) == 0 || payload[0] != '{' {
		return nil, errNestedToken
	}

	tok, err := jwt.Parse([]byte(raw),
		jwt.WithKeySet(set, jws.WithInferAlgorithmFromKey(true)),
		jwt.WithValidate(true),
		jwt.WithAcceptableSkew(clockSkew),
		jwt.WithRequiredClaim(jwt.ExpirationKey),
	)
	if err != nil {
		return nil, fmt.Errorf("verify jwt: %w", err)
	}
	if sub, ok := tok.Subject(); !ok || sub == "" {
		return nil, errSubMissing
	}
	return tok, nil
}

// checkAlgorithm applies the algorithm policy to the first signature's
// header. jwx verifies with the key's own alg when the key has one, so that
// alg must equal the (allowed) header alg; without a key alg, jwx only
// tries algorithms that match both the key type and the header.
func checkAlgorithm(set jwk.Set, hdr jws.Headers) error {
	alg, ok := hdr.Algorithm()
	if !ok {
		return errAlgNotAllowed
	}
	if _, allowed := allowedAlgorithms[alg.String()]; !allowed {
		return errAlgNotAllowed
	}
	kid, ok := hdr.KeyID()
	if !ok || kid == "" {
		return errKidMissing
	}
	key, ok := set.LookupKeyID(kid)
	if !ok {
		return nil // jwx reports the unknown kid
	}
	if keyAlg, ok := key.Algorithm(); ok && keyAlg.String() != alg.String() {
		return errAlgKeyMismatch
	}
	return nil
}
