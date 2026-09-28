// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaskingRuleTargetsLoadFromYAML(t *testing.T) {
	cfg, err := Load(writeYAML(t, `
clusters:
  - name: prod
    brokers: ["b:9092"]
    data_masking:
      - topics: ["^orders$"]
        fields: ["$.email"]
      - topics: ["^orders$"]
        targets: [key, headers]
        headers: ["(?i)^authorization$"]
        regex:
          - match: ".+"
`))
	require.NoError(t, err)
	rules := cfg.Clusters[0].DataMasking
	require.Len(t, rules, 2)
	assert.Nil(t, rules[0].Targets)
	assert.Equal(t, []string{MaskTargetValue}, rules[0].MaskTargets(), "a rule without targets masks the value only")
	assert.Equal(t, []string{MaskTargetKey, MaskTargetHeaders}, rules[1].MaskTargets())
	assert.Equal(t, []string{"(?i)^authorization$"}, rules[1].Headers)
}

func TestMaskingRuleValidationFailsStartup(t *testing.T) {
	cases := map[string]struct {
		rule    string
		wantErr string
	}{
		"unknown target": {`{targets: [value, body], fields: ["$.x"]}`,
			`clusters[0] (prod): data_masking[0]: targets: unknown target "body" (use value|key|headers)`},
		"headers without headers target": {`{targets: [key], headers: ["auth"], regex: [{match: "x"}]}`,
			`clusters[0] (prod): data_masking[0]: headers: selects header keys, but targets does not include "headers"`},
		"bad header pattern": {`{targets: [headers], headers: ["("], regex: [{match: "x"}]}`,
			`clusters[0] (prod): data_masking[0]: headers: pattern "("`},
		"bad regex": {`{regex: [{match: "["}]}`,
			`clusters[0] (prod): data_masking[0]: regex: match "["`},
		"bad topic pattern": {`{topics: ["("], fields: ["$.x"]}`,
			`clusters[0] (prod): data_masking[0]: topics: pattern "("`},
		"bad jsonpath": {`{fields: ["$[?("]}`,
			`clusters[0] (prod): data_masking[0]: fields: jsonpath "$[?("`},
	}
	for name, tc := range cases {
		_, err := Load(writeYAML(t, "clusters:\n  - name: prod\n    brokers: [\"b:9092\"]\n    data_masking:\n      - "+tc.rule+"\n"))
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.wantErr, name)
	}
}
