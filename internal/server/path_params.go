// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
)

// pathParam returns the path parameter key exactly as the generated binding
// binds it, so middleware (RBAC, private-cluster handling, logging) acts on
// the same name as the handler.
//
// chi routes on r.URL.RawPath when it is set, and its parameters are then
// still percent-encoded; the generated wrappers unescape them with
// url.PathUnescape (runtime.BindStyledParameterWithOptions with
// ValueIsUnescaped: r.URL.RawPath == ""). Otherwise chi routed on the
// decoded r.URL.Path and the value is used as is. Decoding happens once, so
// "a%252Fb" becomes "a%2Fb", never "a/b".
func pathParam(r *http.Request, key string) (string, error) {
	v := chi.URLParam(r, key)
	if r.URL.RawPath == "" {
		return v, nil
	}
	return url.PathUnescape(v)
}

// setPathParam replaces the path parameter key so that pathParam and the
// generated binding both return value.
func setPathParam(r *http.Request, key, value string) {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return
	}
	if r.URL.RawPath != "" {
		value = url.PathEscape(value)
	}
	for i := len(rctx.URLParams.Keys) - 1; i >= 0; i-- {
		if rctx.URLParams.Keys[i] == key {
			rctx.URLParams.Values[i] = value
			return
		}
	}
}

// invalidPathParam returns the name of the first route parameter that is
// not valid percent-encoding or is empty, or "" if all are valid. An empty
// name must never reach RBAC, where it would mean "any name". server.New
// cleans the path, so chi does not deliver empty parameters ("//"
// collapses); this is the check that holds without it.
func invalidPathParam(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return ""
	}
	for _, k := range rctx.URLParams.Keys {
		if k == "" || k == "*" {
			continue
		}
		if v, err := pathParam(r, k); err != nil || v == "" {
			return k
		}
	}
	return ""
}

func writeInvalidPathParam(w http.ResponseWriter, key string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{
		"error": `invalid parameter "` + key + `"`,
		"code":  invalidRequestCode,
	})
}
