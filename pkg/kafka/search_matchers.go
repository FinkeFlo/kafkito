// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"
	"github.com/dop251/goja"
	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"
)

type matcher interface {
	match(m *Message) (bool, error)
}

type passAllMatcher struct{}

func (passAllMatcher) match(*Message) (bool, error) { return true, nil }

type containsMatcher struct {
	needle string
	zones  []SearchZone
}

func (c *containsMatcher) match(m *Message) (bool, error) {
	n := c.needle
	for _, z := range c.zones {
		switch z {
		case ZoneValue:
			if strings.Contains(m.Value, n) {
				return true, nil
			}
		case ZoneKey:
			if strings.Contains(m.Key, n) {
				return true, nil
			}
		case ZoneHeaders:
			for k, v := range m.Headers {
				if strings.Contains(k, n) || strings.Contains(v, n) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// pathEval abstracts over JSONPath/XPath evaluation: returns the list of
// stringified results found when applying the expression to value.
type pathEval func(value string) ([]string, bool, error) // results, parsed OK, err

type pathMatcher struct {
	eval   pathEval
	op     SearchOp
	value  string
	valNum float64
	numOK  bool
	rx     *regexp.Regexp
}

func newPathMatcher(ev pathEval, op SearchOp, value string) (*pathMatcher, error) {
	if op == "" {
		op = OpExists
	}
	pm := &pathMatcher{eval: ev, op: op, value: value}
	switch op {
	case OpRegex:
		rx, err := regexp.Compile(value)
		if err != nil {
			return nil, fmt.Errorf("regex %q: %w", value, err)
		}
		pm.rx = rx
	case OpGt, OpLt, OpGte, OpLte:
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("numeric op %s needs number, got %q", op, value)
		}
		pm.valNum = n
		pm.numOK = true
	case OpExists, OpEq, OpNeq, OpContains:
	default:
		return nil, fmt.Errorf("unknown operator: %s", op)
	}
	return pm, nil
}

func (p *pathMatcher) match(m *Message) (bool, error) {
	results, ok, err := p.eval(m.Value)
	if !ok {
		return false, err // err may be nil (unparseable = skip) or a parse error
	}
	if p.op == OpExists {
		return len(results) > 0, nil
	}
	for _, r := range results {
		if p.compare(r) {
			return true, nil
		}
	}
	return false, nil
}

func (p *pathMatcher) compare(got string) bool {
	switch p.op {
	case OpEq:
		return got == p.value
	case OpNeq:
		return got != p.value
	case OpContains:
		return strings.Contains(got, p.value)
	case OpRegex:
		return p.rx.MatchString(got)
	case OpGt, OpLt, OpGte, OpLte:
		n, err := strconv.ParseFloat(got, 64)
		if err != nil {
			return false
		}
		switch p.op {
		case OpGt:
			return n > p.valNum
		case OpLt:
			return n < p.valNum
		case OpGte:
			return n >= p.valNum
		case OpLte:
			return n <= p.valNum
		}
	}
	return false
}

func jsonPathEval(expr jp.Expr) pathEval {
	return func(value string) ([]string, bool, error) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
			return nil, false, nil // skip silently
		}
		parsed, err := oj.ParseString(trimmed)
		if err != nil {
			return nil, false, err
		}
		nodes := expr.Get(parsed)
		out := make([]string, 0, len(nodes))
		for _, n := range nodes {
			out = append(out, stringifyJSONValue(n))
		}
		return out, true, nil
	}
}

func stringifyJSONValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}

func xmlPathEval(expr *xpath.Expr) pathEval {
	return func(value string) ([]string, bool, error) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || trimmed[0] != '<' {
			return nil, false, nil
		}
		doc, err := xmlquery.Parse(strings.NewReader(trimmed))
		if err != nil {
			return nil, false, err
		}
		result := expr.Evaluate(xmlquery.CreateXPathNavigator(doc))
		out := []string{}
		switch v := result.(type) {
		case *xpath.NodeIterator:
			for v.MoveNext() {
				out = append(out, v.Current().Value())
			}
		case bool:
			out = append(out, strconv.FormatBool(v))
		case float64:
			out = append(out, strconv.FormatFloat(v, 'f', -1, 64))
		case string:
			out = append(out, v)
		default:
			out = append(out, fmt.Sprint(v))
		}
		return out, true, nil
	}
}

// resolveRange picks the [start, end) offset window per partition from the

// jsMatcher evaluates a user-provided boolean JavaScript expression per message
// using goja. The expression has access to: key (string), value (string), parsed
// (any, the JSON-parsed value if parseable, otherwise null), headers (object),
// partition (number), offset (number), timestampMs (number).
type jsMatcher struct {
	prog *goja.Program
	src  string
}

func newJSMatcher(script string) (*jsMatcher, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return nil, errors.New("js filter requires a non-empty expression")
	}
	// Wrap as a function body. We accept either a full statement (e.g.
	// `return value.includes("x")`) or a single expression.
	body := script
	if !strings.Contains(body, "return") {
		body = "return (" + body + ");"
	}
	wrapped := "(function(key, value, parsed, headers, partition, offset, timestampMs){ " + body + " })"
	prog, err := goja.Compile("kafkito-filter.js", wrapped, true)
	if err != nil {
		return nil, fmt.Errorf("js filter compile: %w", err)
	}
	return &jsMatcher{prog: prog, src: script}, nil
}

func (j *jsMatcher) match(m *Message) (bool, error) {
	rt := goja.New()
	// Hard limit script execution to ~100ms per message.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-time.After(100 * time.Millisecond):
			rt.Interrupt("kafkito-filter timeout")
		case <-done:
		}
	}()

	v, err := rt.RunProgram(j.prog)
	if err != nil {
		return false, fmt.Errorf("js filter run: %w", err)
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		return false, errors.New("js filter did not compile to a function")
	}
	var parsed any
	if m.Value != "" {
		_ = json.Unmarshal([]byte(m.Value), &parsed)
	}
	headers := make(map[string]string, len(m.Headers))
	for k, v := range m.Headers {
		headers[k] = v
	}
	res, err := fn(goja.Undefined(),
		rt.ToValue(m.Key),
		rt.ToValue(m.Value),
		rt.ToValue(parsed),
		rt.ToValue(headers),
		rt.ToValue(m.Partition),
		rt.ToValue(m.Offset),
		rt.ToValue(m.Timestamp),
	)
	if err != nil {
		return false, fmt.Errorf("js filter run: %w", err)
	}
	return res.ToBoolean(), nil
}
