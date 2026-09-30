// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

// adhoc_test.go covers the HMAC-keyed fingerprint used for ad-hoc cluster
// client dedup (CodeQL go/weak-sensitive-data-hashing remediation): the
// digest must depend on the config, stay stable across calls with the same
// key, and change if the key changes, so a bare-hash regression would be
// caught here. Its input must cover every setting that changes the
// connection or how kafkito treats the cluster, is_prod included, and
// encode them unambiguously, so two different definitions never share an
// entry.

import (
	"crypto/hmac"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
)

var fpTestKey = []byte("fixed-test-key-0123456789012345")

func sampleClusterConfig() config.ClusterConfig {
	return config.ClusterConfig{
		Name:    "irrelevant-to-fingerprint",
		Brokers: []string{"b2:9092", "b1:9092"},
		Auth: config.AuthConfig{
			Type:     "sasl-plain",
			Username: "alice",
			Password: "s3cr3t",
		},
	}
}

func TestFingerprint_StableForSameConfigAndKey(t *testing.T) {
	t.Parallel()

	cfg := sampleClusterConfig()

	got1 := Fingerprint(cfg, fpTestKey)
	got2 := Fingerprint(cfg, fpTestKey)

	assert.Equal(t, got1, got2, "same config + same key must yield the same fingerprint")
	assert.NotEmpty(t, got1)
}

func TestFingerprint_IgnoresNameAndDataMasking(t *testing.T) {
	t.Parallel()

	cfg := sampleClusterConfig()
	relabelled := cfg
	relabelled.Name = "totally-different-display-name"
	relabelled.DataMasking = []config.MaskingRule{{Fields: []string{"$.email"}}}

	assert.Equal(t, Fingerprint(cfg, fpTestKey), Fingerprint(relabelled, fpTestKey),
		"Name and DataMasking must not affect the fingerprint")
}

// Different settings never share a fingerprint, including pairs that only
// differ in where one value ends and the next one begins.
func TestFingerprint_DistinguishesSettings(t *testing.T) {
	t.Parallel()

	brokers := func(b ...string) config.ClusterConfig {
		return config.ClusterConfig{Brokers: b}
	}
	auth := func(user, pass string) config.ClusterConfig {
		return config.ClusterConfig{
			Brokers: []string{"b:9092"},
			Auth:    config.AuthConfig{Type: "plain", Username: user, Password: pass},
		}
	}
	schemaRegistry := func(url, user, pass string) config.ClusterConfig {
		return config.ClusterConfig{
			Brokers:        []string{"b:9092"},
			SchemaRegistry: config.SchemaRegistryConfig{URL: url, Username: user, Password: pass},
		}
	}
	prod := brokers("b:9092")
	prod.IsProd = true

	cases := []struct {
		name string
		a, b config.ClusterConfig
	}{
		{"is_prod", brokers("b:9092"), prod},
		{"broker order", brokers("a:9092", "b:9092"), brokers("b:9092", "a:9092")},
		{"one broker or two", brokers("ab"), brokers("a", "b")},
		{"comma in a broker", brokers("a,b"), brokers("a", "b")},
		{"space in a broker", brokers("a b"), brokers("a", "b")},
		{"username and password split", auth("ab", "c"), auth("a", "bc")},
		{"field label in a username", auth("a\nauth.pass=b", "c"), auth("a", "b\nauth.pass=c")},
		{"schema registry username and password split", schemaRegistry("http://sr", "ab", "c"), schemaRegistry("http://sr", "a", "bc")},
		{"schema registry url and username split", schemaRegistry("http://sr", "u", ""), schemaRegistry("http://sru", "", "")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.NotEqual(t, Fingerprint(tc.a, fpTestKey), Fingerprint(tc.b, fpTestKey))
		})
	}
}

// fingerprintIgnoredFields are the ClusterConfig fields that do not change a
// private cluster: UseAdhoc replaces Name with the internal name, and
// private clusters never mask.
var fingerprintIgnoredFields = []string{"Name", "DataMasking"}

// Every ClusterConfig field, nested ones included, changes the digest or is
// deliberately ignored. A new field fails here until adhocDigest covers it
// or it is added to fingerprintIgnoredFields.
func TestAdhocDigest_CoversEveryClusterConfigField(t *testing.T) {
	t.Parallel()

	base := adhocDigest(config.ClusterConfig{}, fpTestKey)
	var visited []string
	var visit func(prefix string, typ reflect.Type, index []int)
	visit = func(prefix string, typ reflect.Type, index []int) {
		for i := range typ.NumField() {
			field := typ.Field(i)
			path := prefix + field.Name
			fieldIndex := append(slices.Clone(index), i)
			if field.Type.Kind() == reflect.Struct {
				visit(path+".", field.Type, fieldIndex)
				continue
			}
			visited = append(visited, path)

			var cfg config.ClusterConfig
			v := reflect.ValueOf(&cfg).Elem().FieldByIndex(fieldIndex)
			switch field.Type.Kind() {
			case reflect.String:
				v.SetString("x")
			case reflect.Bool:
				v.SetBool(true)
			case reflect.Slice:
				v.Set(reflect.MakeSlice(field.Type, 1, 1))
			default:
				t.Fatalf("%s: no test value for kind %s", path, field.Type.Kind())
			}
			changed := !hmac.Equal(base, adhocDigest(cfg, fpTestKey))
			if slices.Contains(fingerprintIgnoredFields, path) {
				assert.Falsef(t, changed, "%s is ignored but changes the digest", path)
			} else {
				assert.Truef(t, changed, "%s does not change the digest: add it to adhocDigest or to fingerprintIgnoredFields", path)
			}
		}
	}
	visit("", reflect.TypeFor[config.ClusterConfig](), nil)
	assert.Subset(t, visited, []string{"IsProd", "Brokers", "Auth.Password", "TLS.InsecureSkipVerify", "SchemaRegistry.URL"})
}

func TestFingerprint_DiffersForDifferentConfig(t *testing.T) {
	t.Parallel()

	cfg := sampleClusterConfig()
	other := cfg
	other.Auth.Password = "different-password"

	assert.NotEqual(t, Fingerprint(cfg, fpTestKey), Fingerprint(other, fpTestKey),
		"a different password must produce a different fingerprint")
}

func TestFingerprint_DiffersForDifferentKey(t *testing.T) {
	t.Parallel()

	cfg := sampleClusterConfig()
	keyA := []byte("key-a-0123456789012345678901234")
	keyB := []byte("key-b-0123456789012345678901234")

	assert.NotEqual(t, Fingerprint(cfg, keyA), Fingerprint(cfg, keyB),
		"the digest must be keyed: changing the key must change the output "+
			"even for an identical config (this is what distinguishes the fix "+
			"from a bare unkeyed hash)")
}

func TestRegistry_AdhocFPKey_LazyStableAndRandomPerRegistry(t *testing.T) {
	t.Parallel()

	r1 := NewRegistry(nil, slog.Default())
	r2 := NewRegistry(nil, slog.Default())

	k1a := r1.adhocFPKey()
	k1b := r1.adhocFPKey()
	assert.Equal(t, k1a, k1b, "adhocFPKey must be stable across calls on the same registry")
	require.Len(t, k1a, 32)

	k2 := r2.adhocFPKey()
	assert.NotEqual(t, k1a, k2, "each registry must get its own random key")
}

func TestRegistry_UseAdhoc_DedupsIdenticalConfigsViaFingerprint(t *testing.T) {
	t.Parallel()

	r := NewRegistry(nil, slog.Default())
	cfg := config.ClusterConfig{Name: "n1", Brokers: []string{"b1:9092"}}

	name1, err := r.UseAdhoc(cfg)
	require.NoError(t, err)
	name2, err := r.UseAdhoc(cfg)
	require.NoError(t, err)

	assert.Equal(t, name1, name2, "identical configs must dedup to the same internal name")
	assert.True(t, config.IsAdhocClusterName(name1))

	diff := cfg
	diff.Brokers = []string{"b2:9092"}
	name3, err := r.UseAdhoc(diff)
	require.NoError(t, err)
	assert.NotEqual(t, name1, name3, "a different config must get a different internal name")
}

// Private clusters never carry masking, whatever data_masking their config
// sends: the user brings their own credentials and sees the raw data.
func TestUseAdhoc_IgnoresDataMasking(t *testing.T) {
	t.Parallel()
	reg := NewRegistry(nil, slog.Default())
	t.Cleanup(reg.Close)
	cfg := sampleClusterConfig()
	cfg.DataMasking = []config.MaskingRule{
		{Fields: []string{"$.email"}},
		{Targets: []string{config.MaskTargetKey, config.MaskTargetHeaders}, Regex: []config.RegexMask{{Match: ".+"}}},
	}

	name, err := reg.UseAdhoc(cfg)
	require.NoError(t, err)

	p := reg.MaskingPolicy(name)
	assert.True(t, p.IsEmpty())
	assert.False(t, p.AppliesTo("orders"))
}

// Identical settings share one entry and one client, whatever display name
// or masking rules came with them; the stored config keeps neither.
func TestUseAdhoc_IdenticalSettingsShareOneEntry(t *testing.T) {
	t.Parallel()
	reg := NewRegistry(nil, slog.New(slog.DiscardHandler))
	t.Cleanup(reg.Close)
	first := config.ClusterConfig{
		Name:        "mine",
		Brokers:     []string{"shared.invalid:9092"},
		Auth:        config.AuthConfig{Type: "plain", Username: "alice", Password: "s3cr3t"},
		DataMasking: []config.MaskingRule{{Fields: []string{"$.email"}}},
	}
	second := first
	second.Name = "theirs"
	second.DataMasking = nil

	name1, err := reg.UseAdhoc(first)
	require.NoError(t, err)
	cl1, err := reg.Client(name1)
	require.NoError(t, err)
	name2, err := reg.UseAdhoc(second)
	require.NoError(t, err)
	cl2, err := reg.Client(name2)
	require.NoError(t, err)

	assert.Equal(t, name1, name2)
	assert.Same(t, cl1, cl2, "identical settings must share the client")
	stored, ok := reg.ConfigFor(name1)
	require.True(t, ok)
	assert.Equal(t, name1, stored.Name)
	assert.Nil(t, stored.DataMasking)
}

// is_prod is part of the definition: whichever variant of a connection is
// registered first, each variant gets its own entry and ConfigFor, which
// the production confirmation reads, reports its own flag.
func TestUseAdhoc_IsProdGetsItsOwnEntry(t *testing.T) {
	t.Parallel()
	plain := config.ClusterConfig{Brokers: []string{"shared.invalid:9092"}}
	prod := plain
	prod.IsProd = true

	for _, prodFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("prod first %v", prodFirst), func(t *testing.T) {
			t.Parallel()
			reg := NewRegistry(nil, slog.New(slog.DiscardHandler))
			t.Cleanup(reg.Close)
			order := []config.ClusterConfig{plain, prod, plain, prod}
			if prodFirst {
				slices.Reverse(order)
			}

			names := map[bool]string{}
			for _, cfg := range order {
				name, err := reg.UseAdhoc(cfg)
				require.NoError(t, err)
				if prev, ok := names[cfg.IsProd]; ok {
					require.Equal(t, prev, name, "the same definition must keep its entry")
				}
				names[cfg.IsProd] = name
			}
			require.NotEqual(t, names[false], names[true])
			for isProd, name := range names {
				got, ok := reg.ConfigFor(name)
				require.True(t, ok)
				assert.Equal(t, isProd, got.IsProd)
			}
		})
	}
}

// An entry is reused only if its full digest matches. A definition whose
// internal name is taken by other settings is refused and leaves that
// entry as it is: not reused, not replaced, not kept alive.
func TestUseAdhoc_RefusesNameRegisteredForOtherSettings(t *testing.T) {
	t.Parallel()
	mine := config.ClusterConfig{Brokers: []string{"mine.invalid:9092"}}
	other := config.ClusterConfig{Brokers: []string{"other.invalid:9092"}, IsProd: true}

	cases := []struct {
		name string
		// occupy makes name answer to another definition.
		occupy func(t *testing.T, reg *Registry, name string)
	}{
		{
			name: "entry of another definition",
			occupy: func(t *testing.T, reg *Registry, name string) {
				t.Helper()
				otherName, err := reg.UseAdhoc(other)
				require.NoError(t, err)
				reg.mu.Lock()
				defer reg.mu.Unlock()
				reg.clusters[name] = reg.clusters[otherName]
				reg.masking[name] = reg.masking[otherName]
				reg.adhocLastUsed[name] = reg.adhocLastUsed[otherName]
				reg.adhocDigests[name] = reg.adhocDigests[otherName]
			},
		},
		{
			name: "cluster without a digest",
			occupy: func(t *testing.T, reg *Registry, name string) {
				t.Helper()
				reg.mu.Lock()
				defer reg.mu.Unlock()
				reg.clusters[name] = other
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clock := newFakeClock()
			reg := clockedRegistry(t, clock, time.Hour)
			name := config.AdhocClusterPrefix + Fingerprint(mine, reg.adhocFPKey())
			tc.occupy(t, reg, name)
			reg.mu.Lock()
			lastUsed, hadLastUsed := reg.adhocLastUsed[name]
			reg.mu.Unlock()
			clock.Advance(time.Minute)

			got, err := reg.UseAdhoc(mine)
			require.ErrorIs(t, err, errAdhocNameInUse)
			assert.Empty(t, got)

			reg.mu.Lock()
			stored := reg.clusters[name]
			nowLastUsed, hasLastUsed := reg.adhocLastUsed[name]
			reg.mu.Unlock()
			assert.Equal(t, other.Brokers, stored.Brokers, "the existing entry must not be replaced")
			assert.True(t, stored.IsProd)
			assert.Equal(t, hadLastUsed, hasLastUsed)
			assert.Equal(t, lastUsed, nowLastUsed, "a refused registration must not count as use")
		})
	}
}

// Eviction drops the digest with the entry, and the same definition
// registers again afterwards.
func TestUseAdhoc_RegistersAgainAfterEviction(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	reg := clockedRegistry(t, clock, time.Hour)
	cfg := config.ClusterConfig{Brokers: []string{"again.invalid:9092"}, IsProd: true}

	name, err := reg.UseAdhoc(cfg)
	require.NoError(t, err)
	clock.Advance(adhocIdleTTL)
	reg.evictIdleAdhoc()
	require.False(t, reg.registered(name))
	reg.mu.Lock()
	_, digestKept := reg.adhocDigests[name]
	reg.mu.Unlock()
	assert.False(t, digestKept, "eviction must drop the digest")

	again, err := reg.UseAdhoc(cfg)
	require.NoError(t, err)
	assert.Equal(t, name, again)
	got, ok := reg.ConfigFor(again)
	require.True(t, ok)
	assert.True(t, got.IsProd)
}
