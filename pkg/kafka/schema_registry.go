// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/FinkeFlo/kafkito/pkg/config"
	"golang.org/x/sync/errgroup"
)

// ErrNoSchemaRegistry is returned when SR features are requested but the
// cluster has no schema_registry.url configured.
var ErrNoSchemaRegistry = errors.New("schema registry not configured for cluster")

// Subject represents a Schema Registry subject with its available versions.
type Subject struct {
	Name     string `json:"name"`
	Versions []int  `json:"versions"`
	// LatestSchemaType is the schemaType of the latest version (AVRO/JSON/PROTOBUF).
	// Empty when the registry didn't return one or the latest version probe failed.
	LatestSchemaType string `json:"latest_schema_type,omitempty"`
}

// SchemaVersion is a single version of a subject's schema.
type SchemaVersion struct {
	Subject    string            `json:"subject"`
	ID         int               `json:"id"`
	Version    int               `json:"version"`
	SchemaType string            `json:"schemaType"`
	Schema     string            `json:"schema"`
	References []SchemaReference `json:"references,omitempty"`
	Config     *SubjectConfig    `json:"config,omitempty"`
}

// SchemaReference is a reference to another schema.
type SchemaReference struct {
	Name    string `json:"name"`
	Subject string `json:"subject"`
	Version int    `json:"version"`
}

// SubjectConfig captures subject-level compatibility config.
type SubjectConfig struct {
	CompatibilityLevel string `json:"compatibilityLevel,omitempty"`
}

// RegisterSchemaRequest is used by POST /subjects/{s}/versions.
type RegisterSchemaRequest struct {
	Schema     string            `json:"schema"`
	SchemaType string            `json:"schemaType,omitempty"` // AVRO (default), JSON, PROTOBUF
	References []SchemaReference `json:"references,omitempty"`
}

// RegisterSchemaResponse is returned by the registry on register.
type RegisterSchemaResponse struct {
	ID int `json:"id"`
}

// SchemaRegistryClient wraps the Confluent-compatible REST API.
type SchemaRegistryClient struct {
	base string
	cfg  config.SchemaRegistryConfig
	http *http.Client
}

func newSchemaRegistryClient(cfg config.SchemaRegistryConfig) *SchemaRegistryClient {
	tr := &http.Transport{}
	if strings.HasPrefix(cfg.URL, "https://") && cfg.InsecureSkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in
	}
	return &SchemaRegistryClient{
		base: strings.TrimRight(cfg.URL, "/"),
		cfg:  cfg,
		http: &http.Client{Timeout: 10 * time.Second, Transport: tr},
	}
}

// SchemaRegistry returns a configured client for the cluster, or an error
// if SR is not configured.
func (r *Registry) SchemaRegistry(cluster string) (*SchemaRegistryClient, error) {
	cc, ok := r.clusters[cluster]
	if !ok {
		return nil, ErrUnknownCluster
	}
	if strings.TrimSpace(cc.SchemaRegistry.URL) == "" {
		return nil, ErrNoSchemaRegistry
	}
	return newSchemaRegistryClient(cc.SchemaRegistry), nil
}

func (c *SchemaRegistryClient) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.schemaregistry.v1+json, application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/vnd.schemaregistry.v1+json")
	}
	if c.cfg.Username != "" {
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 400 {
		data, _ := io.ReadAll(res.Body)
		// SR errors look like {"error_code":40401,"message":"..."}
		var srErr struct {
			Code    int    `json:"error_code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &srErr); err == nil && srErr.Message != "" {
			return fmt.Errorf("sr %d: %s", res.StatusCode, srErr.Message)
		}
		return fmt.Errorf("sr %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// ListSubjects returns all subjects registered in SR.
func (c *SchemaRegistryClient) ListSubjects(ctx context.Context) ([]string, error) {
	var out []string
	if err := c.do(ctx, http.MethodGet, "/subjects", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListVersions returns the versions of a subject.
func (c *SchemaRegistryClient) ListVersions(ctx context.Context, subject string) ([]int, error) {
	var out []int
	p := "/subjects/" + url.PathEscape(subject) + "/versions"
	if err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetVersion returns a specific version of a subject's schema. version may
// also be "latest".
func (c *SchemaRegistryClient) GetVersion(ctx context.Context, subject, version string) (*SchemaVersion, error) {
	var out SchemaVersion
	p := "/subjects/" + url.PathEscape(subject) + "/versions/" + url.PathEscape(version)
	if err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	// Best-effort: attach subject compatibility config.
	cfg, err := c.SubjectConfig(ctx, subject)
	if err == nil {
		out.Config = cfg
	}
	return &out, nil
}

// SubjectConfig fetches the subject-level compatibility (falls back to global).
func (c *SchemaRegistryClient) SubjectConfig(ctx context.Context, subject string) (*SubjectConfig, error) {
	var out SubjectConfig
	p := "/config/" + url.PathEscape(subject)
	err := c.do(ctx, http.MethodGet, p, nil, &out)
	if err != nil && strings.Contains(err.Error(), "40401") {
		// subject has no override; read global
		err = c.do(ctx, http.MethodGet, "/config", nil, &out)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSubject soft-deletes all versions of a subject. Set permanent=true for
// a hard delete (must soft-delete first in Confluent SR).
func (c *SchemaRegistryClient) DeleteSubject(ctx context.Context, subject string, permanent bool) ([]int, error) {
	var out []int
	p := "/subjects/" + url.PathEscape(subject)
	if permanent {
		p += "?permanent=true"
	}
	if err := c.do(ctx, http.MethodDelete, p, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RegisterSchema registers a new schema version under the subject.
func (c *SchemaRegistryClient) RegisterSchema(ctx context.Context, subject string, req RegisterSchemaRequest) (*RegisterSchemaResponse, error) {
	var out RegisterSchemaResponse
	p := "/subjects/" + url.PathEscape(subject) + "/versions"
	if err := c.do(ctx, http.MethodPost, p, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSchemaByID resolves a schema by its global id.
//
// Confluent SR returns only `{"schema":..., "schemaType":..., "subject":..., "version":...}`
// from `/schemas/ids/{id}`; we additionally fetch a representative subject so
// the decoded message can carry meaningful metadata.
func (c *SchemaRegistryClient) GetSchemaByID(ctx context.Context, id int) (*SchemaVersion, error) {
	var raw struct {
		Schema     string            `json:"schema"`
		SchemaType string            `json:"schemaType"`
		References []SchemaReference `json:"references,omitempty"`
		Subject    string            `json:"subject,omitempty"`
		Version    int               `json:"version,omitempty"`
	}
	p := fmt.Sprintf("/schemas/ids/%d", id)
	if err := c.do(ctx, http.MethodGet, p, nil, &raw); err != nil {
		return nil, err
	}
	out := &SchemaVersion{
		ID:         id,
		Schema:     raw.Schema,
		SchemaType: raw.SchemaType,
		References: raw.References,
		Subject:    raw.Subject,
		Version:    raw.Version,
	}
	if out.Subject == "" {
		// Older Confluent versions don't echo subject/version on /schemas/ids;
		// fall back to /schemas/ids/{id}/subjects.
		var subjects []string
		_ = c.do(ctx, http.MethodGet, p+"/subjects", nil, &subjects)
		if len(subjects) > 0 {
			out.Subject = subjects[0]
		}
	}
	return out, nil
}

// ListSubjectsWithVersions returns subjects with versions and the schemaType
// of each subject's latest version. Per-subject probes (ListVersions plus a
// single GetVersion("latest")) run in parallel with bounded concurrency to
// keep the one-shot UI list page responsive even with hundreds of subjects.
// Errors on individual subjects degrade gracefully — the subject is still
// returned with whatever fields succeeded.
func (c *SchemaRegistryClient) ListSubjectsWithVersions(ctx context.Context) ([]Subject, error) {
	subs, err := c.ListSubjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Subject, len(subs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	var mu sync.Mutex
	for i, s := range subs {
		i, s := i, s
		g.Go(func() error {
			sub := Subject{Name: s}
			if vs, err := c.ListVersions(gctx, s); err == nil {
				sub.Versions = vs
			}
			if v, err := c.GetVersion(gctx, s, "latest"); err == nil {
				sub.LatestSchemaType = v.SchemaType
			}
			mu.Lock()
			out[i] = sub
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	return out, nil
}
