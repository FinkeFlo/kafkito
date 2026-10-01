// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAdhocClusterName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		want bool
	}{
		{AdhocClusterPrefix + "0123456789abcdef", true},
		{AdhocClusterPrefix, true},
		{"", false},
		{PrivateClusterSentinel, false},
		{"prod", false},
		{"x" + AdhocClusterPrefix + "0123456789abcdef", false},
		{"__ADHOC_0123456789abcdef", false},
	} {
		assert.Equal(t, tc.want, IsAdhocClusterName(tc.name), "%q", tc.name)
	}
}

func TestValidate_PrivateClusterNamesReserved(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		reserved bool
	}{
		{PrivateClusterSentinel, true},
		{AdhocClusterPrefix + "0123456789abcdef", true},
		{AdhocClusterPrefix, true},
		{"prod", false},
		{"adhoc", false},
	} {
		err := Config{Clusters: []ClusterConfig{{Name: tc.name, Brokers: []string{"b:9092"}}}}.Validate()
		if tc.reserved {
			assert.ErrorContains(t, err, "reserved for private clusters", tc.name)
		} else {
			assert.NoError(t, err, tc.name)
		}
	}
}

func TestPublicClusterName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, want string }{
		{AdhocClusterPrefix + "0123456789abcdef", PrivateClusterSentinel},
		{AdhocClusterPrefix, PrivateClusterSentinel},
		{PrivateClusterSentinel, PrivateClusterSentinel},
		{"prod", "prod"},
		{"", ""},
		{"x" + AdhocClusterPrefix + "0123456789abcdef", "x" + AdhocClusterPrefix + "0123456789abcdef"},
	} {
		assert.Equal(t, tc.want, PublicClusterName(tc.name), "%q", tc.name)
	}
}

func TestClusterLogName(t *testing.T) {
	t.Parallel()

	internal := AdhocClusterPrefix + "0123456789abcdef"
	for _, tc := range []struct{ name, want string }{
		{internal, "private-0f3f15bc7c80"},
		{"prod", "prod"},
		{PrivateClusterSentinel, PrivateClusterSentinel},
		{"", ""},
	} {
		assert.Equal(t, tc.want, ClusterLogName(tc.name), "%q", tc.name)
	}

	other := AdhocClusterPrefix + "fedcba9876543210"
	for _, name := range []string{internal, other, AdhocClusterPrefix} {
		id := ClusterLogName(name)
		assert.Regexp(t, `^private-[0-9a-f]{12}$`, id, name)
		assert.False(t, IsAdhocClusterName(id), "%s is no registry name", id)
		assert.Equal(t, id, ClusterLogName(id), "a log id maps to itself")
	}
	assert.NotEqual(t, ClusterLogName(internal), ClusterLogName(other))
}

const rbacEnabledYAML = "rbac:\n  enabled: true\n"

func privateClustersYAML(mode string) string {
	return "private_clusters:\n  mode: " + mode + "\n"
}

func TestPrivateClustersMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		env  string
		want PrivateClusterMode
	}{
		{name: "default", want: PrivateClustersOn},
		{name: "yaml on", yaml: privateClustersYAML("on"), want: PrivateClustersOn},
		{name: "yaml off", yaml: privateClustersYAML("off"), want: PrivateClustersOff},
		{name: "yaml role", yaml: privateClustersYAML("role") + rbacEnabledYAML, want: PrivateClustersRole},
		{name: "yaml quoted", yaml: privateClustersYAML(`"on"`), want: PrivateClustersOn},
		{name: "yaml case and space", yaml: privateClustersYAML(`" OFF "`), want: PrivateClustersOff},
		{name: "yaml upper case", yaml: privateClustersYAML("Role") + rbacEnabledYAML, want: PrivateClustersRole},
		{name: "yaml true", yaml: privateClustersYAML("true"), want: PrivateClustersOn},
		{name: "yaml false", yaml: privateClustersYAML("false"), want: PrivateClustersOff},
		{name: "yaml False", yaml: privateClustersYAML("False"), want: PrivateClustersOff},
		{name: "yaml empty", yaml: privateClustersYAML(`""`), want: PrivateClustersOn},
		{name: "env off", env: "off", want: PrivateClustersOff},
		{name: "env trimmed and case", env: " ROLE ", yaml: rbacEnabledYAML, want: PrivateClustersRole},
		{name: "env beats yaml", yaml: privateClustersYAML("off"), env: "on", want: PrivateClustersOn},
		{name: "env beats yaml boolean", yaml: privateClustersYAML("false"), env: "on", want: PrivateClustersOn},
		{name: "empty env keeps yaml", yaml: privateClustersYAML("off"), env: " ", want: PrivateClustersOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateEnv(t)
			if tc.env != "" {
				t.Setenv("KAFKITO_PRIVATE_CLUSTERS", tc.env)
			}
			path := ""
			if tc.yaml != "" {
				path = writeYAML(t, tc.yaml)
			}

			cfg, err := Load(path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.PrivateClusters.EffectiveMode())
		})
	}
}

func TestPrivateClustersModeRejected(t *testing.T) {
	for _, tc := range []struct {
		name    string
		yaml    string
		env     string
		wantErr string
	}{
		{name: "yaml unknown", yaml: privateClustersYAML("yes"), wantErr: `private_clusters.mode "yes" not supported (use on|off|role)`},
		{name: "yaml number", yaml: privateClustersYAML("1"), wantErr: `private_clusters.mode "1" not supported (use on|off|role)`},
		{name: "yaml quoted boolean", yaml: privateClustersYAML(`"true"`), wantErr: `private_clusters.mode "true" not supported (use on|off|role)`},
		{name: "env unknown", env: "disabled", wantErr: `private_clusters.mode "disabled" not supported (use on|off|role)`},
		{name: "env boolean", env: "false", wantErr: `private_clusters.mode "false" not supported (use on|off|role)`},
		{name: "yaml role without rbac", yaml: privateClustersYAML("role"), wantErr: `private_clusters.mode "role" requires rbac.enabled: true`},
		{name: "env role without rbac", env: "role", wantErr: `private_clusters.mode "role" requires rbac.enabled: true`},
		{name: "env role with rbac disabled", env: "role", yaml: "rbac:\n  enabled: false\n", wantErr: `private_clusters.mode "role" requires rbac.enabled: true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateEnv(t)
			if tc.env != "" {
				t.Setenv("KAFKITO_PRIVATE_CLUSTERS", tc.env)
			}
			path := ""
			if tc.yaml != "" {
				path = writeYAML(t, tc.yaml)
			}

			_, err := Load(path)
			require.Error(t, err)
			assert.EqualError(t, err, tc.wantErr)
		})
	}
}

func TestValidate_PrivateClustersMode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		mode        PrivateClusterMode
		rbacEnabled bool
		wantErr     bool
	}{
		{mode: "", wantErr: false},
		{mode: PrivateClustersOn, wantErr: false},
		{mode: PrivateClustersOff, wantErr: false},
		{mode: PrivateClustersRole, rbacEnabled: true, wantErr: false},
		{mode: PrivateClustersRole, wantErr: true},
		{mode: "sometimes", rbacEnabled: true, wantErr: true},
	} {
		cfg := Config{PrivateClusters: PrivateClustersConfig{Mode: tc.mode}, RBAC: RBACConfig{Enabled: tc.rbacEnabled}}
		err := cfg.Validate()
		if tc.wantErr {
			assert.Error(t, err, "mode %q rbac %v", tc.mode, tc.rbacEnabled)
		} else {
			assert.NoError(t, err, "mode %q rbac %v", tc.mode, tc.rbacEnabled)
		}
	}
}

func allowPlainYAML(v string) string {
	return "private_clusters:\n  allow_plain_without_tls: " + v + "\n"
}

func TestPrivateClustersAllowPlainWithoutTLS(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		env  string
		want bool
	}{
		{name: "default", want: false},
		{name: "yaml true", yaml: allowPlainYAML("true"), want: true},
		{name: "yaml false", yaml: allowPlainYAML("false"), want: false},
		{name: "yaml quoted", yaml: allowPlainYAML(`"true"`), want: true},
		{name: "yaml empty", yaml: allowPlainYAML(`""`), want: false},
		{name: "yaml null", yaml: allowPlainYAML(""), want: false},
		{name: "yaml next to mode", yaml: "private_clusters:\n  mode: on\n  allow_plain_without_tls: true\n", want: true},
		{name: "env true", env: "true", want: true},
		{name: "env trimmed", env: " TRUE ", want: true},
		{name: "env 1", env: "1", want: true},
		{name: "env false", env: "false", want: false},
		{name: "env beats yaml", yaml: allowPlainYAML("true"), env: "false", want: false},
		{name: "empty env keeps yaml", yaml: allowPlainYAML("true"), env: " ", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateEnv(t)
			if tc.env != "" {
				t.Setenv("KAFKITO_PRIVATE_CLUSTERS_ALLOW_PLAIN_WITHOUT_TLS", tc.env)
			}
			path := ""
			if tc.yaml != "" {
				path = writeYAML(t, tc.yaml)
			}

			cfg, err := Load(path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.PrivateClusters.AllowPlainWithoutTLS)
			assert.Equal(t, PrivateClustersOn, cfg.PrivateClusters.EffectiveMode())
		})
	}
}

func TestPrivateClustersAllowPlainWithoutTLSRejected(t *testing.T) {
	for _, tc := range []struct {
		name    string
		yaml    string
		env     string
		wantErr string
	}{
		{name: "yaml word", yaml: allowPlainYAML("yes"), wantErr: `private_clusters.allow_plain_without_tls "yes" is not a boolean (use true|false)`},
		{name: "yaml number", yaml: allowPlainYAML("2"), wantErr: `private_clusters.allow_plain_without_tls 2 is not a boolean (use true|false)`},
		{name: "yaml list", yaml: allowPlainYAML("[true]"), wantErr: `private_clusters.allow_plain_without_tls [true] is not a boolean (use true|false)`},
		{name: "env word", env: "enabled", wantErr: `private_clusters.allow_plain_without_tls "enabled" is not a boolean (use true|false)`},
		{name: "env beats valid yaml", yaml: allowPlainYAML("true"), env: "on", wantErr: `private_clusters.allow_plain_without_tls "on" is not a boolean (use true|false)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateEnv(t)
			if tc.env != "" {
				t.Setenv("KAFKITO_PRIVATE_CLUSTERS_ALLOW_PLAIN_WITHOUT_TLS", tc.env)
			}
			path := ""
			if tc.yaml != "" {
				path = writeYAML(t, tc.yaml)
			}

			_, err := Load(path)
			require.Error(t, err)
			assert.EqualError(t, err, tc.wantErr)
		})
	}
}

// A scalar private_clusters block stays an error: parsing the opt-out must
// not replace it with a map holding only the opt-out.
func TestPrivateClustersBlockNotAMap(t *testing.T) {
	isolateEnv(t)
	t.Setenv("KAFKITO_PRIVATE_CLUSTERS_ALLOW_PLAIN_WITHOUT_TLS", "false")

	_, err := Load(writeYAML(t, "private_clusters: off\n"))
	require.Error(t, err)
	assert.EqualError(t, err, "private_clusters must be a block with mode and allow_plain_without_tls, not off")
}
