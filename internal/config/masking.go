// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package config

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/ohler55/ojg/jp"
)

// Record parts a MaskingRule can target.
const (
	MaskTargetValue   = "value"
	MaskTargetKey     = "key"
	MaskTargetHeaders = "headers"
)

// MaskTargets returns the record parts r masks: its Targets, or the value
// alone when Targets is empty.
func (r MaskingRule) MaskTargets() []string {
	if len(r.Targets) == 0 {
		return []string{MaskTargetValue}
	}
	return r.Targets
}

// Validate reports the first problem that would keep r from compiling: an
// unknown target, a headers selector without the headers target, or a
// malformed topic/header pattern, JSONPath or regex.
func (r MaskingRule) Validate() error {
	for _, t := range r.Targets {
		switch t {
		case MaskTargetValue, MaskTargetKey, MaskTargetHeaders:
		default:
			return fmt.Errorf("targets: unknown target %q (use value|key|headers)", t)
		}
	}
	if len(r.Headers) > 0 && !slices.Contains(r.Targets, MaskTargetHeaders) {
		return fmt.Errorf("headers: selects header keys, but targets does not include %q", MaskTargetHeaders)
	}
	for _, p := range r.Topics {
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("topics: pattern %q: %w", p, err)
		}
	}
	for _, p := range r.Headers {
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("headers: pattern %q: %w", p, err)
		}
	}
	for _, f := range r.Fields {
		if _, err := jp.ParseString(f); err != nil {
			return fmt.Errorf("fields: jsonpath %q: %w", f, err)
		}
	}
	for _, rg := range r.Regex {
		if _, err := regexp.Compile(rg.Match); err != nil {
			return fmt.Errorf("regex: match %q: %w", rg.Match, err)
		}
	}
	return nil
}
