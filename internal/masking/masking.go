// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

// Package masking applies per-cluster data masking rules to the value, key
// and header values of Kafka records. Rules are compiled once and applied
// serially: JSONPath field replacement on JSON payloads, then regex
// substitution on the resulting string.
package masking

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"

	"github.com/FinkeFlo/kafkito/internal/config"
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

	value, key, headers bool
	// headerPatterns narrows the headers target to matching header keys;
	// empty means every header.
	headerPatterns []*regexp.Regexp
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
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}
		cr := compiledRule{replacement: r.Replacement}
		if cr.replacement == "" {
			cr.replacement = defaultReplacement
		}
		targets := r.MaskTargets()
		cr.value = slices.Contains(targets, config.MaskTargetValue)
		cr.key = slices.Contains(targets, config.MaskTargetKey)
		cr.headers = slices.Contains(targets, config.MaskTargetHeaders)
		for _, tp := range r.Topics {
			rx, err := regexp.Compile(tp)
			if err != nil {
				return nil, fmt.Errorf("rule %d: topic pattern %q: %w", i, tp, err)
			}
			cr.topicPatterns = append(cr.topicPatterns, rx)
		}
		for _, hp := range r.Headers {
			rx, err := regexp.Compile(hp)
			if err != nil {
				return nil, fmt.Errorf("rule %d: header pattern %q: %w", i, hp, err)
			}
			cr.headerPatterns = append(cr.headerPatterns, rx)
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

// AppliesTo reports whether at least one rule is active for topic, i.e.
// whether Apply, ApplyKey or ApplyHeader may change records of that topic.
func (p *Policy) AppliesTo(topic string) bool {
	return !p.IsEmpty() && len(p.activeRules(topic, func(compiledRule) bool { return true })) > 0
}

// Apply masks a record value according to the value rules that match topic.
// Returns the new value and whether anything was masked.
func (p *Policy) Apply(topic, value string) (string, bool) {
	return p.apply(topic, value, func(r compiledRule) bool { return r.value })
}

// ApplyKey masks a record key according to the key rules that match topic.
// Returns the new key and whether anything was masked.
func (p *Policy) ApplyKey(topic, key string) (string, bool) {
	return p.apply(topic, key, func(r compiledRule) bool { return r.key })
}

// ApplyHeader masks the value of header name according to the header rules
// that match topic and select name. Returns the new header value and whether
// anything was masked. Header keys themselves are never masked.
func (p *Policy) ApplyHeader(topic, name, value string) (string, bool) {
	return p.apply(topic, value, func(r compiledRule) bool { return r.selectsHeader(name) })
}

func (r compiledRule) selectsHeader(name string) bool {
	if !r.headers {
		return false
	}
	if len(r.headerPatterns) == 0 {
		return true
	}
	for _, rx := range r.headerPatterns {
		if rx.MatchString(name) {
			return true
		}
	}
	return false
}

func (p *Policy) apply(topic, s string, targeted func(compiledRule) bool) (string, bool) {
	if p.IsEmpty() || s == "" {
		return s, false
	}
	active := p.activeRules(topic, targeted)
	if len(active) == 0 {
		return s, false
	}
	masked := false

	var parsed any
	jsonish := json.Unmarshal([]byte(s), &parsed) == nil
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
				s = string(b)
			}
		}
	}

	for _, r := range active {
		for _, rg := range r.regexes {
			if rg.rx.MatchString(s) {
				s = rg.rx.ReplaceAllString(s, rg.repl)
				masked = true
			}
		}
	}
	return s, masked
}

func (p *Policy) activeRules(topic string, targeted func(compiledRule) bool) []compiledRule {
	out := make([]compiledRule, 0, len(p.rules))
	for _, r := range p.rules {
		if !targeted(r) {
			continue
		}
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
