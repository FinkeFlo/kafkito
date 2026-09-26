// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/rbac"
	"github.com/go-chi/chi/v5"
)

// ProdConfirmHeader is the request header the frontend must set to "true"
// to perform a mutating/dangerous operation (produce, delete topic, delete
// records, reset offsets) against a cluster marked is_prod. This is the
// server-side enforcement point: the frontend's confirmation dialog is a UX
// nicety, not a security boundary — a direct API call without this header
// against a production cluster is always rejected, regardless of what the
// caller's local cluster list says.
const ProdConfirmHeader = "X-Kafkito-Confirm-Prod"

// prodConfirmationError returns a 428 Precondition Required error when the
// named cluster is marked is_prod and the caller did not set
// ProdConfirmHeader: true, and nil when the request may proceed. Unknown
// clusters are allowed through here; the caller's own lookup
// (Client/Admin/etc.) will report ErrUnknownCluster as usual.
func prodConfirmationError(reg interface {
	ConfigFor(name string) (config.ClusterConfig, bool)
}, cluster string, r *http.Request) *apiError {
	cfg, ok := reg.ConfigFor(cluster)
	if !ok || !cfg.IsProd {
		return nil
	}
	if strings.EqualFold(r.Header.Get(ProdConfirmHeader), "true") {
		return nil
	}
	return &apiError{
		Status:  http.StatusPreconditionRequired,
		Code:    "production_confirmation_required",
		Message: "production cluster: resend with " + ProdConfirmHeader + ": true after user confirmation",
	}
}

// requireProdConfirmation is prodConfirmationError for the hand-written
// handlers: it writes the 428 and returns false, or returns true if the
// request may proceed.
func (a *clusterAPI) requireProdConfirmation(w http.ResponseWriter, r *http.Request, cluster string) bool {
	if err := prodConfirmationError(a.reg, cluster, r); err != nil {
		writeJSON(w, err.Status, map[string]string{"error": err.Message, "code": err.Code})
		return false
	}
	return true
}

// clusterAPI wires cluster- and topic-related endpoints.
type clusterAPI struct {
	reg    *kafkapkg.Registry
	policy *rbac.Policy
	log    *slog.Logger
}

func (a *clusterAPI) mount(r chi.Router) {
	r.Get("/clusters/{cluster}/groups", a.listGroups)
	r.Post("/clusters/{cluster}/groups", a.createGroup)
	r.Get("/clusters/{cluster}/groups/{group}", a.describeGroup)
	r.Delete("/clusters/{cluster}/groups/{group}", a.deleteGroup)
	r.Post("/clusters/{cluster}/groups/{group}/reset-offsets", a.resetGroupOffsets)
	r.Get("/clusters/{cluster}/schemas/subjects", a.listSubjects)
	r.Get("/clusters/{cluster}/schemas/subjects/{subject}/versions", a.listVersions)
	r.Get("/clusters/{cluster}/schemas/subjects/{subject}/versions/{version}", a.getSchemaVersion)
	r.Post("/clusters/{cluster}/schemas/subjects/{subject}/versions", a.registerSchema)
	r.Delete("/clusters/{cluster}/schemas/subjects/{subject}", a.deleteSubject)
	r.Get("/clusters/{cluster}/acls", a.listACLs)
	r.Post("/clusters/{cluster}/acls", a.createACL)
	r.Delete("/clusters/{cluster}/acls", a.deleteACL)
	r.Get("/clusters/{cluster}/users", a.listSCRAMUsers)
	r.Post("/clusters/{cluster}/users", a.upsertSCRAMUser)
	r.Delete("/clusters/{cluster}/users/{user}", a.deleteSCRAMUser)
}

// listGroups returns the consumer groups of a cluster.
func (a *clusterAPI) listGroups(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	groups, err := a.reg.ListGroups(ctx, cluster)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		gatewayError(ctx, w, a.log, "list groups", err)
		return
	}
	if a.policy != nil && a.policy.Enabled() {
		user := rbacSubject(r, a.policy)
		groups = filterGroupsByRBAC(groups, a.policy, user, cluster)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster": cluster,
		"groups":  groups,
	})
}

// describeGroup returns detail for a consumer group.
func (a *clusterAPI) describeGroup(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	group := chi.URLParam(r, "group")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	d, err := a.reg.DescribeGroup(ctx, cluster, group)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		gatewayError(ctx, w, a.log, "describe group", err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func injectKafkitoProduceHeaders(req *kafkapkg.ProduceRequest, user string) {
	if req.Headers == nil {
		req.Headers = make(map[string]string)
	}
	req.Headers["X-Kafkito-Source"] = "true"
	if user != "" {
		req.Headers["X-Kafkito-User"] = user
	}
}

func isClientProduceErr(msg string) bool {
	return strings.Contains(msg, "invalid base64") ||
		strings.Contains(msg, "unsupported encoding")
}

// isInvalidPartitionErr reports whether a produce failed because the requested
// partition does not exist on the topic. franz-go raises this from the
// partitioner (see internal/kafka's explicitOrKeyPartitioner), so the wording comes
// from kgo rather than kafkito.
func isInvalidPartitionErr(msg string) bool {
	return strings.Contains(msg, "invalid record partitioning choice")
}

// isACLClientErr reports whether a Kafka ACL error originates from bad
// caller-supplied input (400) rather than a broker-side failure (502).
// Used by createACL and deleteACL.
func isACLClientErr(msg string) bool {
	return strings.Contains(msg, "required") || strings.Contains(msg, "validate") ||
		strings.Contains(msg, "resource_type") || strings.Contains(msg, "pattern_type") ||
		strings.Contains(msg, "operation") || strings.Contains(msg, "permission_type")
}

// isSCRAMClientErr reports whether a Kafka SCRAM error originates from bad
// caller-supplied input (400) rather than a broker-side failure (502).
// Used by upsertSCRAMUser and deleteSCRAMUser.
func isSCRAMClientErr(msg string) bool {
	return strings.Contains(msg, "required") || strings.Contains(msg, "mechanism") ||
		strings.Contains(msg, "iterations")
}

// isSearchClientErr reports whether a search error originates from bad
// caller-supplied input (400) rather than a broker-side failure (502).
// Used by searchMessages.
func isSearchClientErr(msg string) bool {
	return strings.Contains(msg, "jsonpath") || strings.Contains(msg, "xpath") ||
		strings.Contains(msg, "regex") || strings.Contains(msg, "numeric op") ||
		strings.Contains(msg, "unknown search mode") || strings.Contains(msg, "unknown operator") ||
		strings.Contains(msg, "js filter")
}

// resetGroupOffsets issues an offset reset for a single group+topic.
func (a *clusterAPI) resetGroupOffsets(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	group := chi.URLParam(r, "group")

	if !a.requireProdConfirmation(w, r, cluster) {
		return
	}

	var req kafkapkg.ResetOffsetsRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, err := a.reg.ResetOffsets(ctx, cluster, group, req)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		msg := err.Error()
		if strings.Contains(msg, "required") || strings.Contains(msg, "unknown strategy") || strings.Contains(msg, "not found") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kafka: " + msg})
			return
		}
		gatewayError(ctx, w, a.log, "reset group offsets", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// createGroup creates a new consumer group bound to a single topic.
func (a *clusterAPI) createGroup(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")

	var req kafkapkg.CreateGroupRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, err := a.reg.CreateGroup(ctx, cluster, req)
	if err != nil {
		switch {
		case errors.Is(err, kafkapkg.ErrUnknownCluster):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		case errors.Is(err, kafkapkg.ErrGroupExists):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "kafka: " + err.Error()})
			return
		case errors.Is(err, kafkapkg.ErrNotAuthorized):
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		status := http.StatusBadGateway
		msg := err.Error()
		if strings.Contains(msg, "required") || strings.Contains(msg, "unknown strategy") ||
			strings.Contains(msg, "not found") || strings.Contains(msg, "shift-by") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": "kafka: " + msg})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// deleteGroup removes a consumer group.
func (a *clusterAPI) deleteGroup(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	group := chi.URLParam(r, "group")

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := a.reg.DeleteGroup(ctx, cluster, group); err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		gatewayError(ctx, w, a.log, "delete group", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": group})
}

// --- Schema Registry handlers ---

func (a *clusterAPI) srClient(w http.ResponseWriter, r *http.Request, cluster string) *kafkapkg.SchemaRegistryClient {
	sr, err := a.reg.SchemaRegistry(cluster)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return nil
		}
		if errors.Is(err, kafkapkg.ErrNoSchemaRegistry) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "schema registry not configured for cluster: " + cluster})
			return nil
		}
		gatewayError(r.Context(), w, a.log, "schema registry client", err)
		return nil
	}
	return sr
}

func (a *clusterAPI) listSubjects(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	sr := a.srClient(w, r, cluster)
	if sr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	subs, err := sr.ListSubjectsWithVersions(ctx)
	if err != nil {
		gatewayError(ctx, w, a.log, "list subjects", err)
		return
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i].Name < subs[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster":  cluster,
		"subjects": subs,
	})
}

func (a *clusterAPI) listVersions(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	subject := chi.URLParam(r, "subject")
	sr := a.srClient(w, r, cluster)
	if sr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	vs, err := sr.ListVersions(ctx, subject)
	if err != nil {
		gatewayError(ctx, w, a.log, "list versions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subject": subject, "versions": vs})
}

func (a *clusterAPI) getSchemaVersion(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	subject := chi.URLParam(r, "subject")
	version := chi.URLParam(r, "version")
	sr := a.srClient(w, r, cluster)
	if sr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	v, err := sr.GetVersion(ctx, subject, version)
	if err != nil {
		gatewayError(ctx, w, a.log, "get schema version", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *clusterAPI) registerSchema(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	subject := chi.URLParam(r, "subject")
	sr := a.srClient(w, r, cluster)
	if sr == nil {
		return
	}
	var req kafkapkg.RegisterSchemaRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, err := sr.RegisterSchema(ctx, subject, req)
	if err != nil {
		gatewayError(ctx, w, a.log, "register schema", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *clusterAPI) deleteSubject(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	subject := chi.URLParam(r, "subject")
	sr := a.srClient(w, r, cluster)
	if sr == nil {
		return
	}
	permanent := r.URL.Query().Get("permanent") == "true"
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	versions, err := sr.DeleteSubject(ctx, subject, permanent)
	if err != nil {
		gatewayError(ctx, w, a.log, "delete subject", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": subject, "versions": versions, "permanent": permanent})
}

// listACLs enumerates visible ACLs on the cluster.
func (a *clusterAPI) listACLs(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	acls, err := a.reg.ListACLs(ctx, cluster)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		gatewayError(ctx, w, a.log, "list ACLs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cluster": cluster, "acls": acls})
}

// createACL creates a single ACL entry on the cluster.
func (a *clusterAPI) createACL(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	var spec kafkapkg.ACLSpec
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&spec); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := a.reg.CreateACL(ctx, cluster, spec); err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		msg := err.Error()
		if isACLClientErr(msg) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kafka: " + msg})
			return
		}
		gatewayError(ctx, w, a.log, "create ACL", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "acl": spec})
}

// deleteACL removes ACL entries matching the supplied filter.
func (a *clusterAPI) deleteACL(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	var spec kafkapkg.ACLSpec
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&spec); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	deleted, err := a.reg.DeleteACL(ctx, cluster, spec)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		msg := err.Error()
		if isACLClientErr(msg) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kafka: " + msg})
			return
		}
		gatewayError(ctx, w, a.log, "delete ACL", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
}

// listSCRAMUsers returns all users with SCRAM credentials.
func (a *clusterAPI) listSCRAMUsers(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	users, err := a.reg.ListSCRAMUsers(ctx, cluster)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		gatewayError(ctx, w, a.log, "list SCRAM users", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cluster": cluster, "users": users})
}

type scramUpsertReq struct {
	User       string `json:"user"`
	Mechanism  string `json:"mechanism"`
	Password   string `json:"password"`
	Iterations int32  `json:"iterations"`
}

// upsertSCRAMUser creates or updates a SCRAM credential.
func (a *clusterAPI) upsertSCRAMUser(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	var req scramUpsertReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := a.reg.UpsertSCRAMUser(ctx, cluster, req.User, req.Mechanism, req.Password, req.Iterations); err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		msg := err.Error()
		if isSCRAMClientErr(msg) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kafka: " + msg})
			return
		}
		gatewayError(ctx, w, a.log, "upsert SCRAM user", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": req.User, "mechanism": req.Mechanism})
}

// deleteSCRAMUser deletes a SCRAM credential for the user. Mechanism is a query
// parameter (?mechanism=SCRAM-SHA-256); if omitted, both mechanisms are tried.
func (a *clusterAPI) deleteSCRAMUser(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	user := chi.URLParam(r, "user")
	mechanism := r.URL.Query().Get("mechanism")
	mechs := []string{mechanism}
	if strings.TrimSpace(mechanism) == "" {
		mechs = []string{"SCRAM-SHA-256", "SCRAM-SHA-512"}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	deleted := 0
	var lastErr error
	for _, m := range mechs {
		if err := a.reg.DeleteSCRAMUser(ctx, cluster, user, m); err != nil {
			lastErr = err
			continue
		}
		deleted++
	}
	if deleted == 0 && lastErr != nil {
		if errors.Is(lastErr, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		msg := lastErr.Error()
		if isSCRAMClientErr(msg) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kafka: " + msg})
			return
		}
		gatewayError(ctx, w, a.log, "delete SCRAM user", lastErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": user, "deleted": deleted})
}

// filterTopicsByRBAC removes topics from the list the user is not allowed to
// view. When the user has '*' access, the list is returned unchanged.
func filterTopicsByRBAC(topics []kafkapkg.TopicInfo, policy *rbac.Policy, user, cluster string) []kafkapkg.TopicInfo {
	globs, all := policy.AllowedResourceNames(user, cluster, "topic", "view")
	if all {
		return topics
	}
	if len(globs) == 0 {
		return []kafkapkg.TopicInfo{}
	}
	out := make([]kafkapkg.TopicInfo, 0, len(topics))
	for _, t := range topics {
		for _, glob := range globs {
			if rbac.MatchName(glob, t.Name) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// filterGroupsByRBAC removes consumer groups the user is not allowed to view.
func filterGroupsByRBAC(groups []kafkapkg.GroupInfo, policy *rbac.Policy, user, cluster string) []kafkapkg.GroupInfo {
	globs, all := policy.AllowedResourceNames(user, cluster, "group", "view")
	if all {
		return groups
	}
	if len(globs) == 0 {
		return []kafkapkg.GroupInfo{}
	}
	out := make([]kafkapkg.GroupInfo, 0, len(groups))
	for _, g := range groups {
		for _, glob := range globs {
			if rbac.MatchName(glob, g.GroupID) {
				out = append(out, g)
				break
			}
		}
	}
	return out
}
