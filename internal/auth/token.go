// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"bytes"
	"errors"
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

// Token check errors. Their text names the rule, never a claim or header
// value, so callers may log them. Errors from jwx are mapped onto them and
// not wrapped, because jwx messages can carry the kid or a URL.
var (
	// ErrTokenEmpty rejects an empty bearer token.
	ErrTokenEmpty = errors.New("empty bearer token")
	// ErrTokenMalformed rejects a token that is not a parsable JWS.
	ErrTokenMalformed = errors.New("token is not a well-formed JWS")
	// ErrTokenSignatures rejects a JWS without exactly one signature.
	ErrTokenSignatures = errors.New("token must carry exactly one signature")
	// ErrAlgNotAllowed rejects a header alg outside the asymmetric allow-list.
	ErrAlgNotAllowed = errors.New("token alg not allowed")
	// ErrAlgKeyMismatch rejects a header alg that differs from the key's alg.
	ErrAlgKeyMismatch = errors.New("token alg does not match the key alg")
	// ErrKidMissing rejects a token without a kid header.
	ErrKidMissing = errors.New("token has no kid")
	// ErrNestedToken rejects a payload that is not a JSON claim set.
	ErrNestedToken = errors.New("token payload is not a JSON object")
	// ErrTokenInvalid rejects a token whose signature does not verify against
	// the key set, for example because no usable key matches its kid.
	ErrTokenInvalid = errors.New("token signature verification failed")
	// ErrExpMissing rejects a token without an exp claim.
	ErrExpMissing = errors.New("exp claim missing")
	// ErrTokenExpired rejects a token past its exp.
	ErrTokenExpired = errors.New("token expired")
	// ErrTokenNotYetValid rejects a token before its nbf.
	ErrTokenNotYetValid = errors.New("token not yet valid (nbf)")
	// ErrTokenIssuedAt rejects a token whose iat lies in the future.
	ErrTokenIssuedAt = errors.New("token iat is in the future")
	// ErrTokenClaims rejects a claim set that fails any other validation.
	ErrTokenClaims = errors.New("token claims invalid")
	// ErrSubMissing rejects a token without a non-empty sub claim.
	ErrSubMissing = errors.New("sub claim missing")
	// ErrIssuerMismatch rejects a token whose iss is not the expected issuer.
	ErrIssuerMismatch = errors.New("iss does not match the configured issuer")
	// ErrAudienceMissing rejects a token without an aud claim.
	ErrAudienceMissing = errors.New("aud claim missing")
	// ErrAudienceMismatch rejects a token whose aud names no expected audience.
	ErrAudienceMismatch = errors.New("aud does not contain the expected audience")
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
		return nil, ErrTokenMalformed
	}
	if len(msg.Signatures()) != 1 {
		return nil, ErrTokenSignatures
	}
	if err := checkAlgorithm(set, msg.Signatures()[0].ProtectedHeaders()); err != nil {
		return nil, err
	}
	// jwt.Parse would verify a nested JWS payload as well, and that inner
	// signature would bypass checkAlgorithm; accept claim sets only.
	if payload := bytes.TrimSpace(msg.Payload()); len(payload) == 0 || payload[0] != '{' {
		return nil, ErrNestedToken
	}

	tok, err := jwt.Parse([]byte(raw),
		jwt.WithKeySet(set, jws.WithInferAlgorithmFromKey(true)),
		jwt.WithValidate(true),
		jwt.WithAcceptableSkew(clockSkew),
		jwt.WithRequiredClaim(jwt.ExpirationKey),
	)
	if err != nil {
		return nil, jwtError(err)
	}
	if sub, ok := tok.Subject(); !ok || sub == "" {
		return nil, ErrSubMissing
	}
	return tok, nil
}

// jwtError maps a jwt.Parse error onto a token check error. The jwx error
// itself is dropped: its text may name the kid.
func jwtError(err error) error {
	switch {
	case errors.Is(err, jwt.TokenExpiredError()):
		return ErrTokenExpired
	case errors.Is(err, jwt.TokenNotYetValidError()):
		return ErrTokenNotYetValid
	case errors.Is(err, jwt.InvalidIssuedAtError()):
		return ErrTokenIssuedAt
	case errors.Is(err, jwt.MissingRequiredClaimError()):
		return ErrExpMissing // exp is the only claim ParseToken requires
	case errors.Is(err, jwt.ValidateError()):
		return ErrTokenClaims
	default:
		return ErrTokenInvalid
	}
}

// checkAlgorithm applies the algorithm policy to the first signature's
// header. jwx verifies with the key's own alg when the key has one, so that
// alg must equal the (allowed) header alg; without a key alg, jwx only
// tries algorithms that match both the key type and the header.
func checkAlgorithm(set jwk.Set, hdr jws.Headers) error {
	alg, ok := hdr.Algorithm()
	if !ok {
		return ErrAlgNotAllowed
	}
	if _, allowed := allowedAlgorithms[alg.String()]; !allowed {
		return ErrAlgNotAllowed
	}
	kid, ok := hdr.KeyID()
	if !ok || kid == "" {
		return ErrKidMissing
	}
	key, ok := set.LookupKeyID(kid)
	if !ok {
		return nil // jwx reports the unknown kid
	}
	if keyAlg, ok := key.Algorithm(); ok && keyAlg.String() != alg.String() {
		return ErrAlgKeyMismatch
	}
	return nil
}
