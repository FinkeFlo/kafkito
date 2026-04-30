// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

// Package masking applies per-cluster data masking rules to Kafka record
// values for Consume and Search paths. Rules are compiled once and applied
// serially: JSONPath field replacement on JSON payloads, then regex
// substitution on the resulting string.
package masking

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/FinkeFlo/kafkito/pkg/config"
	"github.com/ohler55/ojg/jp"
)

const defaultReplacement = "***"

// Policy is a compiled set of masking rules for a single cluster.
type Policy struct {
	rules []compiledRule
}

type compiledRule struct {
	topicPatterns []*regexp.Regexp
	fields        []jp.Expr
	regexes       []compiledRegex
	replacement   string
}

type compiledRegex struct {
	rx   *regexp.Regexp
	repl string
}

// Compile turns configured rules into a Policy. An empty rule set yields an
// empty Policy (check IsEmpty).
func Compile(rules []config.MaskingRule) (*Policy, error) {
	p := &Policy{}
	if len(rules) == 0 {
		return p, nil
	}
	p.rules = make([]compiledRule, 0, len(rules))
	for i, r := range rules {
		cr := compiledRule{replacement: r.Replacement}
		if cr.replacement == "" {
			cr.replacement = defaultReplacement
		}
		for _, tp := range r.Topics {
			rx, err := regexp.Compile(tp)
			if err != nil {
				return nil, fmt.Errorf("rule %d: topic pattern %q: %w", i, tp, err)
			}
			cr.topicPatterns = append(cr.topicPatterns, rx)
		}
		for _, f := range r.Fields {
			expr, err := jp.ParseString(f)
			if err != nil {
				return nil, fmt.Errorf("rule %d: jsonpath %q: %w", i, f, err)
			}
			cr.fields = append(cr.fields, expr)
		}
		for _, rg := range r.Regex {
			rx, err := regexp.Compile(rg.Match)
			if err != nil {
				return nil, fmt.Errorf("rule %d: regex %q: %w", i, rg.Match, err)
			}
			repl := rg.Replacement
			if repl == "" {
				repl = defaultReplacement
			}
			cr.regexes = append(cr.regexes, compiledRegex{rx: rx, repl: repl})
		}
		p.rules = append(p.rules, cr)
	}
	return p, nil
}

// IsEmpty reports whether the policy has zero rules.
func (p *Policy) IsEmpty() bool { return p == nil || len(p.rules) == 0 }

// Apply masks value according to rules that match topic. Returns the new
// value and whether anything was masked.
func (p *Policy) Apply(topic, value string) (string, bool) {
	if p.IsEmpty() || value == "" {
		return value, false
	}
	active := p.activeRules(topic)
	if len(active) == 0 {
		return value, false
	}
	masked := false

	var parsed any
	jsonish := json.Unmarshal([]byte(value), &parsed) == nil
	if jsonish {
		for _, r := range active {
			for _, f := range r.fields {
				hits := f.Get(parsed)
				if len(hits) == 0 {
					continue
				}
				if err := f.Set(parsed, r.replacement); err == nil {
					masked = true
				}
			}
		}
		if masked {
			if b, err := json.Marshal(parsed); err == nil {
				value = string(b)
			}
		}
	}

	for _, r := range active {
		for _, rg := range r.regexes {
			if rg.rx.MatchString(value) {
				value = rg.rx.ReplaceAllString(value, rg.repl)
				masked = true
			}
		}
	}
	return value, masked
}

func (p *Policy) activeRules(topic string) []compiledRule {
	out := make([]compiledRule, 0, len(p.rules))
	for _, r := range p.rules {
		if len(r.topicPatterns) == 0 {
			out = append(out, r)
			continue
		}
		for _, rx := range r.topicPatterns {
			if rx.MatchString(topic) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}
