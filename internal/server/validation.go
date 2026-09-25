// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/FinkeFlo/kafkito/api"
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// invalidRequestCode is the Error.code of requests rejected by the OpenAPI
// request validator.
const invalidRequestCode = "invalid_request"

// loadSpec parses the embedded OpenAPI document once per process.
var loadSpec = sync.OnceValues(func() (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(api.Spec)
	if err != nil {
		return nil, fmt.Errorf("parse openapi spec: %w", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		return nil, fmt.Errorf("validate openapi spec: %w", err)
	}
	return doc, nil
})

// newRequestValidator returns middleware that validates requests against the
// embedded OpenAPI document and reports violations through errs. It is
// mounted per route, after the body limit, on generated operations only.
//
// Authentication is not checked here: the spec's bearerAuth requirement is
// enforced by the auth middleware, so security schemes are accepted as-is.
// Servers are ignored because the public URL differs per deployment, and
// defaults are not injected so handlers see the request exactly as sent.
func newRequestValidator(errs errorWriter) (func(http.Handler) http.Handler, error) {
	doc, err := loadSpec()
	if err != nil {
		return nil, err
	}
	return newDocValidator(doc, errs), nil
}

// newDocValidator builds the request validator of newRequestValidator for
// doc.
func newDocValidator(doc *openapi3.T, errs errorWriter) func(http.Handler) http.Handler {
	// The middleware clears Servers on the document it gets; hand it a
	// shallow copy so the shared document stays untouched.
	cp := *doc
	validate := nethttpmiddleware.OapiRequestValidatorWithOptions(&cp, &nethttpmiddleware.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc:  openapi3filter.NoopAuthenticationFunc,
			SkipSettingDefaults: true,
		},
		DoNotValidateServers:  true,
		SilenceServersWarning: true,
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, r *http.Request, _ nethttpmiddleware.ErrorHandlerOpts) {
			errs.writeError(w, r, err)
		},
	})
	return func(next http.Handler) http.Handler {
		validated := validate(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			validated.ServeHTTP(w, withCleanURLPath(r))
		})
	}
}

// withCleanURLPath returns r with a cleaned URL path. chi's CleanPath only
// cleans the routing path, while the validator matches on r.URL; without
// this, a request chi accepts (e.g. "/api//v1/info") would not be found by
// the validator.
func withCleanURLPath(r *http.Request) *http.Request {
	clean := path.Clean(r.URL.Path)
	if clean == r.URL.Path {
		return r
	}
	u := *r.URL
	u.Path = clean
	if u.RawPath != "" {
		u.RawPath = path.Clean(u.RawPath)
	}
	r2 := new(http.Request)
	*r2 = *r
	r2.URL = &u
	return r2
}

// noRequestBody discards the body of operations that declare none. Handlers
// never read it and the validator would otherwise buffer it unbounded.
func noRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			_ = r.Body.Close()
		}
		r.Body = http.NoBody
		r.GetBody = nil
		r.ContentLength = 0
		next.ServeHTTP(w, r)
	})
}

// limitRequestBody caps the request body at limit bytes before anything
// (validator or handler) reads it. Exceeding the limit fails the read with an
// *apiError carrying status and "invalid body: http: request body too large".
func limitRequestBody(limit int64, status int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = &limitedBody{rc: http.MaxBytesReader(w, r.Body, limit), status: status}
				r.GetBody = nil
			}
			next.ServeHTTP(w, r)
		})
	}
}

type limitedBody struct {
	rc     io.ReadCloser
	status int
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return n, &apiError{Status: b.status, Message: "invalid body: " + mbe.Error(), Err: mbe}
	}
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }

// requestValidationError maps errors of the OpenAPI request validator and the
// generated parameter binding to a 4xx apiError, or returns nil for other
// errors. Messages name the parameter or body field and the violated rule;
// they never contain the submitted value, because validator messages echo
// input verbatim (including header contents such as X-Kafkito-Cluster).
func requestValidationError(err error) *apiError {
	var re *openapi3filter.RequestError
	if errors.As(err, &re) {
		return &apiError{Status: http.StatusBadRequest, Code: invalidRequestCode, Message: describeRequestError(re)}
	}
	var sre *openapi3filter.SecurityRequirementsError
	if errors.As(err, &sre) {
		return &apiError{Status: http.StatusBadRequest, Code: invalidRequestCode, Message: "security requirements not met"}
	}
	switch {
	case errors.Is(err, routers.ErrPathNotFound):
		return &apiError{Status: http.StatusNotFound, Message: "not found"}
	case errors.Is(err, routers.ErrMethodNotAllowed):
		return &apiError{Status: http.StatusMethodNotAllowed, Message: "method not allowed"}
	}
	if name, ok := generatedParamErrorName(err); ok {
		return &apiError{Status: http.StatusBadRequest, Code: invalidRequestCode, Message: fmt.Sprintf("invalid parameter %q", name)}
	}
	return nil
}

// generatedParamErrorName reports the parameter name of a binding error
// raised by the generated chi wrapper.
func generatedParamErrorName(err error) (string, bool) {
	var (
		invalidFormat *gen.InvalidParamFormatError
		required      *gen.RequiredParamError
		requiredHdr   *gen.RequiredHeaderError
		unmarshal     *gen.UnmarshalingParamError
		tooMany       *gen.TooManyValuesForParamError
		cookie        *gen.UnescapedCookieParamError
	)
	switch {
	case errors.As(err, &invalidFormat):
		return invalidFormat.ParamName, true
	case errors.As(err, &required):
		return required.ParamName, true
	case errors.As(err, &requiredHdr):
		return requiredHdr.ParamName, true
	case errors.As(err, &unmarshal):
		return unmarshal.ParamName, true
	case errors.As(err, &tooMany):
		return tooMany.ParamName, true
	case errors.As(err, &cookie):
		return cookie.ParamName, true
	}
	return "", false
}

func describeRequestError(re *openapi3filter.RequestError) string {
	var loc string
	switch {
	case re.Parameter != nil:
		loc = fmt.Sprintf("parameter %q in %s", re.Parameter.Name, re.Parameter.In)
	case re.RequestBody != nil:
		loc = "request body"
	default:
		return "invalid request"
	}
	var se *openapi3.SchemaError
	switch {
	case errors.Is(re.Err, openapi3filter.ErrInvalidRequired):
		return loc + ": is required"
	case errors.Is(re.Err, openapi3filter.ErrInvalidEmptyValue):
		return loc + ": must not be empty"
	case errors.As(re.Err, &se):
		ptr, rule := describeSchemaError(se)
		if ptr != "" && ptr != "/" {
			loc += " " + strconv.Quote(ptr)
		}
		return loc + ": " + rule
	case strings.HasPrefix(re.Reason, "header Content-Type has unexpected value"):
		return loc + ": unsupported Content-Type"
	case re.Reason == "failed to decode request body":
		return loc + ": malformed"
	case re.Reason == "reading failed":
		return loc + ": could not be read"
	}
	var pe *openapi3filter.ParseError
	if errors.As(re.Err, &pe) {
		return loc + ": invalid format"
	}
	return loc + ": invalid"
}

// describeSchemaError returns the JSON pointer of the failing value and a
// description of the violated rule, derived from the schema only.
//
// OpenAPI 3.1 documents are validated with a JSON Schema 2020-12 validator
// whose errors carry nothing but a message of the form
// `error at "<pointer>": <detail>`, with nested causes as a MultiError.
// The detail embeds the offending value, so only its keyword is recognised
// and the rule is rebuilt from the parts that stem from the schema.
func describeSchemaError(se *openapi3.SchemaError) (string, string) {
	se = innermostSchemaError(se)
	if se.SchemaField != "" {
		return jsonPointer(se.JSONPointer()), describeSchemaField(se)
	}
	ptr, detail := splitSchemaReason(se.Reason)
	// A single missing property is reported at its own pointer, like the
	// built-in validator does. The name comes from the schema's required list.
	if name, ok := strings.CutPrefix(detail, "missing property '"); ok && strings.HasSuffix(name, "'") {
		return strings.TrimSuffix(ptr, "/") + jsonPointer([]string{strings.TrimSuffix(name, "'")}), "is required"
	}
	return ptr, describeSchemaDetail(detail)
}

func innermostSchemaError(se *openapi3.SchemaError) *openapi3.SchemaError {
	for se.Origin != nil {
		var me openapi3.MultiError
		if !errors.As(se.Origin, &me) || len(me) == 0 {
			break
		}
		var next *openapi3.SchemaError
		if !errors.As(me[0], &next) {
			break
		}
		se = next
	}
	return se
}

// splitSchemaReason splits a JSON Schema 2020-12 validator message into the
// instance pointer and the keyword detail. Nested causes and root-level
// ones are formatted as:
//
//	error at "<pointer>": at '<pointer>': <detail>
//	at '': <detail>
func splitSchemaReason(reason string) (string, string) {
	ptr := ""
	if rest, ok := strings.CutPrefix(reason, `error at "`); ok {
		if end := strings.Index(rest, `": `); end >= 0 {
			ptr, reason = rest[:end], rest[end+3:]
		}
	}
	if rest, ok := strings.CutPrefix(reason, "at '"); ok {
		if end := strings.Index(rest, "': "); end >= 0 {
			ptr, reason = rest[:end], rest[end+3:]
		}
	}
	return ptr, reason
}

// schemaDetailRules maps the message prefix of a JSON Schema keyword
// violation to the rule text. Only the numeric bound after ", want " is
// kept; "got" parts are the submitted value.
var schemaDetailRules = []struct {
	prefix string
	rule   string
}{
	{"minItems: ", "must have at least %s items"},
	{"maxItems: ", "must have at most %s items"},
	{"minLength: ", "must be at least %s characters long"},
	{"maxLength: ", "must be at most %s characters long"},
	{"minProperties: ", "must have at least %s properties"},
	{"maxProperties: ", "must have at most %s properties"},
	{"minimum: ", "must be >= %s"},
	{"maximum: ", "must be <= %s"},
	{"exclusiveMinimum: ", "must be > %s"},
	{"exclusiveMaximum: ", "must be < %s"},
	{"multipleOf: ", "must be a multiple of %s"},
}

func describeSchemaDetail(detail string) string {
	for _, r := range schemaDetailRules {
		if strings.HasPrefix(detail, r.prefix) {
			if i := strings.LastIndex(detail, ", want "); i >= 0 {
				if want := detail[i+len(", want "):]; isNumber(want) {
					return fmt.Sprintf(r.rule, want)
				}
			}
			return "is out of range"
		}
	}
	switch {
	case strings.HasPrefix(detail, "got ") && strings.Contains(detail, ", want "):
		// Type mismatch: "got <type>, want <type(s)>", both JSON type names.
		want := detail[strings.LastIndex(detail, ", want ")+len(", want "):]
		if isJSONTypeList(want) {
			return "must be of type " + want
		}
		return "has the wrong type"
	case strings.HasPrefix(detail, "value must be one of "), strings.HasPrefix(detail, "'enum' failed"):
		return "must be one of the allowed values"
	case strings.HasPrefix(detail, "value must be "), strings.HasPrefix(detail, "'const' failed"):
		return "must be the allowed constant value"
	case strings.HasPrefix(detail, "missing propert"):
		// The names come from the schema's required list.
		return "is " + strings.Replace(detail, "missing ", "missing required ", 1)
	case strings.HasPrefix(detail, "additional properties "):
		return "has properties that are not allowed"
	case strings.Contains(detail, " does not match pattern "):
		return "must match pattern " + detail[strings.LastIndex(detail, " does not match pattern ")+len(" does not match pattern "):]
	case strings.Contains(detail, " is not valid "):
		return "has an invalid format"
	case strings.HasPrefix(detail, "'oneOf' failed"):
		return "must match exactly one schema"
	case strings.HasPrefix(detail, "'anyOf' failed"):
		return "must match at least one schema"
	case strings.HasPrefix(detail, "'not' failed"), detail == "false schema":
		return "is not allowed"
	}
	return "does not match the schema"
}

// describeSchemaField handles errors of kin-openapi's built-in (OpenAPI 3.0)
// validator, which it falls back to when a schema cannot be compiled as
// JSON Schema 2020-12.
func describeSchemaField(se *openapi3.SchemaError) string {
	s := se.Schema
	if s == nil {
		return "does not match the schema"
	}
	switch se.SchemaField {
	case "type":
		if s.Type != nil {
			return "must be of type " + strings.Join(s.Type.Slice(), " or ")
		}
	case "enum":
		return "must be one of the allowed values"
	case "const":
		return "must be the allowed constant value"
	case "required":
		// The pointer already names the missing property.
		return "is required"
	case "properties", "additionalProperties":
		return "has properties that are not allowed"
	case "pattern":
		return "must match pattern '" + s.Pattern + "'"
	case "format":
		return "has an invalid format"
	case "minItems":
		return fmt.Sprintf("must have at least %d items", s.MinItems)
	case "maxItems":
		if s.MaxItems != nil {
			return fmt.Sprintf("must have at most %d items", *s.MaxItems)
		}
	case "minLength":
		return fmt.Sprintf("must be at least %d characters long", s.MinLength)
	case "maxLength":
		if s.MaxLength != nil {
			return fmt.Sprintf("must be at most %d characters long", *s.MaxLength)
		}
	case "minimum":
		if s.Min != nil {
			return "must be >= " + strconv.FormatFloat(*s.Min, 'g', -1, 64)
		}
	case "maximum":
		if s.Max != nil {
			return "must be <= " + strconv.FormatFloat(*s.Max, 'g', -1, 64)
		}
	case "nullable":
		return "must not be null"
	}
	return "violates " + strconv.Quote(se.SchemaField)
}

func jsonPointer(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(t))
	}
	return b.String()
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func isJSONTypeList(s string) bool {
	for _, t := range strings.Split(s, " or ") {
		switch strings.TrimSpace(t) {
		case "null", "boolean", "object", "array", "number", "string", "integer":
		default:
			return false
		}
	}
	return s != ""
}
