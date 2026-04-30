// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/FinkeFlo/kafkito/pkg/config"
	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
	"github.com/FinkeFlo/kafkito/pkg/rbac"
	"github.com/go-chi/chi/v5"
)

const defaultTestConnectionTimeout = 15 * time.Second

// testConnectionTimeout returns the budget for the user-driven Test
// connection probe. Defaults to 15s; set KAFKITO_TEST_CONNECTION_TIMEOUT
// (e.g. "30s", "2m") to override for slow corporate networks where cold
// DNS + TLS + SASL fan-out across all brokers exceeds the default.
func testConnectionTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("KAFKITO_TEST_CONNECTION_TIMEOUT"))
	if raw == "" {
		return defaultTestConnectionTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultTestConnectionTimeout
	}
	return d
}

// clusterAPI wires cluster- and topic-related endpoints.
type clusterAPI struct {
	reg    *kafkapkg.Registry
	policy *rbac.Policy
}

func (a *clusterAPI) mount(r chi.Router) {
	r.Get("/clusters", a.listClusters)
	r.Post("/clusters/_test", a.testCluster)
	r.Get("/clusters/{cluster}/capabilities", a.getCapabilities)
	r.Post("/clusters/{cluster}/capabilities/refresh", a.refreshCapabilities)
	r.Get("/clusters/{cluster}/brokers", a.listBrokers)
	r.Get("/clusters/{cluster}/topics", a.listTopics)
	r.Post("/clusters/{cluster}/topics", a.createTopic)
	r.Get("/clusters/{cluster}/topics/{topic}", a.describeTopic)
	r.Get("/clusters/{cluster}/topics/{topic}/consumers", a.listTopicConsumers)
	r.Patch("/clusters/{cluster}/topics/{topic}/configs", a.alterTopicConfigs)
	r.Delete("/clusters/{cluster}/topics/{topic}", a.deleteTopic)
	r.Delete("/clusters/{cluster}/topics/{topic}/records", a.deleteRecords)
	r.Get("/clusters/{cluster}/topics/{topic}/messages", a.consumeMessages)
	r.Get("/clusters/{cluster}/topics/{topic}/sample", a.sampleMessages)
	r.Post("/clusters/{cluster}/topics/{topic}/messages/search", a.searchMessages)
	r.Post("/clusters/{cluster}/topics/{topic}/messages", a.produceMessage)
	r.Get("/clusters/{cluster}/groups", a.listGroups)
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

// listClusters returns the configured clusters with a live reachability probe.
func (a *clusterAPI) listClusters(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	out := a.reg.Describe(ctx, 1*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{"clusters": out})
}

// testCluster probes a user-supplied ClusterConfig (sent either as the
// request body or as the X-Kafkito-Cluster header, with body winning) and
// reports reachability plus a short capability probe. Used by the frontend
// settings UI to validate private-cluster credentials before storing them
// in the browser.
func (a *clusterAPI) testCluster(w http.ResponseWriter, r *http.Request) {
	var cfg config.ClusterConfig
	if r.ContentLength > 0 {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPrivateClusterHeaderBytes))
		if err := dec.Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
			return
		}
		if err := validatePrivateClusterConfig(cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	} else if ctxCfg, ok := privateClusterFromContext(r.Context()); ok {
		cfg = ctxCfg
	} else {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "cluster config required in body or " + PrivateClusterHeader + " header",
		})
		return
	}
	name, err := a.reg.UseAdhoc(cfg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Cheap config validation: build the kgo client up-front so that
	// misconfigured TLS / unparseable broker URLs surface as a 400 here
	// rather than burning the full Ping budget. Client construction is
	// synchronous and does not dial; the resulting client is cached on
	// the registry, so the subsequent Ping reuses it.
	if _, cerr := a.reg.Client(name); cerr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": cerr.Error()})
		return
	}
	pingCtx, pingCancel := context.WithTimeout(r.Context(), testConnectionTimeout())
	defer pingCancel()
	info := kafkapkg.ClusterInfo{
		Name:           "",
		AuthType:       strings.ToLower(strings.TrimSpace(cfg.Auth.Type)),
		TLS:            cfg.TLS.Enabled,
		SchemaRegistry: strings.TrimSpace(cfg.SchemaRegistry.URL) != "",
	}
	if info.AuthType == "" {
		info.AuthType = "none"
	}
	if err := a.reg.Ping(pingCtx, name); err != nil {
		info.Reachable = false
		info.Error = err.Error()
	} else {
		info.Reachable = true
		capCtx, capCancel := context.WithTimeout(r.Context(), 4*time.Second)
		if caps, cerr := a.reg.Capabilities(capCtx, name); cerr == nil {
			info.Capabilities = caps
		}
		capCancel()
	}
	writeJSON(w, http.StatusOK, info)
}

// listTopics returns the topics of the named cluster.
//
// Budget is 15s (matches test-connection). Private/browser-stored clusters
// trigger an inline metrics probe inside Registry.ListTopics that can take
// 5–12s on a cold load and is cached for ~30s afterwards.
func (a *clusterAPI) listTopics(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "cluster")

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	topics, err := a.reg.ListTopics(ctx, name)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "unknown cluster: " + name,
			})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "kafka: " + err.Error(),
		})
		return
	}
	sort.Slice(topics, func(i, j int) bool { return topics[i].Name < topics[j].Name })
	if a.policy != nil && a.policy.Enabled() {
		user := r.Header.Get(a.policy.Header())
		topics = filterTopicsByRBAC(topics, a.policy, user, name)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster": name,
		"topics":  topics,
	})
}

// describeTopic returns full detail (partitions, offsets, configs) for a topic.
func (a *clusterAPI) describeTopic(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	topic := chi.URLParam(r, "topic")

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	detail, err := a.reg.DescribeTopic(ctx, cluster, topic)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "unknown cluster: " + cluster,
			})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "kafka: " + err.Error(),
		})
		return
	}
	sort.Slice(detail.Partitions, func(i, j int) bool {
		return detail.Partitions[i].Partition < detail.Partitions[j].Partition
	})
	sort.Slice(detail.Configs, func(i, j int) bool {
		return detail.Configs[i].Name < detail.Configs[j].Name
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster": cluster,
		"topic":   detail,
	})
}

// consumeMessages pulls a bounded batch of messages from a topic.
//
// Query params:
//
//	partition: int32 or -1 for all (default: -1)
//	limit:     1..500              (default: 50)
//	from:      end|start|offset    (default: end)
//	offset:    int64 (used when from=offset)
func (a *clusterAPI) consumeMessages(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	topic := chi.URLParam(r, "topic")

	opts, err := parseConsumeQuery(r.URL.Query())
	if err != nil {
		if writeParamError(w, err) {
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	msgs, err := a.reg.ConsumeMessages(ctx, cluster, topic, opts)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "kafka: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster":  cluster,
		"topic":    topic,
		"messages": msgs,
	})
}

// sampleMessages returns the last n decoded messages for use as a structural
// sample by the topic-search path picker. Reuses the existing consume pipeline
// with hard server-side defaults (from=end, n<=25).
//
// Query params:
//
//	partition: int32 or -1 for all (default: -1)
//	n:         1..25              (default: 5)
func (a *clusterAPI) sampleMessages(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	topic := chi.URLParam(r, "topic")

	opts, err := parseSampleQuery(r.URL.Query())
	if err != nil {
		if writeParamError(w, err) {
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	msgs, err := a.reg.ConsumeMessages(ctx, cluster, topic, opts)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "kafka: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster":    cluster,
		"topic":      topic,
		"messages":   msgs,
		"sampled_at": time.Now().UnixMilli(),
	})
}

// getCapabilities returns the cached capability probe for a cluster.
func (a *clusterAPI) getCapabilities(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	caps, err := a.reg.Capabilities(ctx, cluster)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "kafka: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster":      cluster,
		"capabilities": caps,
	})
}

// refreshCapabilities invalidates the probe cache and re-runs it.
func (a *clusterAPI) refreshCapabilities(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	a.reg.RefreshCapabilities(cluster)
	a.getCapabilities(w, r)
}

// listBrokers returns the brokers of a cluster.
func (a *clusterAPI) listBrokers(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	brokers, err := a.reg.ListBrokers(ctx, cluster)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "kafka: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cluster": cluster, "brokers": brokers})
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
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "kafka: " + err.Error()})
		return
	}
	if a.policy != nil && a.policy.Enabled() {
		user := r.Header.Get(a.policy.Header())
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
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "kafka: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// produceMessage produces a single record to a topic.
func (a *clusterAPI) produceMessage(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	topic := chi.URLParam(r, "topic")

	var req kafkapkg.ProduceRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid body: " + err.Error(),
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	res, err := a.reg.Produce(ctx, cluster, topic, req)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		// Encoding issues are client errors; everything else is 502.
		msg := err.Error()
		status := http.StatusBadGateway
		if isClientProduceErr(msg) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": "kafka: " + msg})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func isClientProduceErr(msg string) bool {
	return strings.Contains(msg, "invalid base64") ||
		strings.Contains(msg, "unsupported encoding")
}

// resetGroupOffsets issues an offset reset for a single group+topic.
func (a *clusterAPI) resetGroupOffsets(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	group := chi.URLParam(r, "group")

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
		status := http.StatusBadGateway
		msg := err.Error()
		if strings.Contains(msg, "required") || strings.Contains(msg, "unknown strategy") || strings.Contains(msg, "not found") {
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
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "kafka: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": group})
}

// createTopic creates a topic on the cluster.
func (a *clusterAPI) createTopic(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")

	var req kafkapkg.CreateTopicRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := a.reg.CreateTopic(ctx, cluster, req); err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		status := http.StatusBadGateway
		msg := err.Error()
		if strings.Contains(msg, "topic name required") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": "kafka: " + msg})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"created": req.Name})
}

// deleteTopic removes a topic.
func (a *clusterAPI) deleteTopic(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	topic := chi.URLParam(r, "topic")

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := a.reg.DeleteTopic(ctx, cluster, topic); err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "kafka: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": topic})
}

// deleteRecords truncates the topic log per partition.
func (a *clusterAPI) deleteRecords(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	topic := chi.URLParam(r, "topic")

	var req kafkapkg.DeleteRecordsRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, err := a.reg.DeleteRecords(ctx, cluster, topic, req)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		status := http.StatusBadGateway
		msg := err.Error()
		if strings.Contains(msg, "required") || strings.Contains(msg, "no resolvable") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": "kafka: " + msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

// --- Schema Registry handlers ---

func (a *clusterAPI) srClient(w http.ResponseWriter, cluster string) *kafkapkg.SchemaRegistryClient {
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
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return nil
	}
	return sr
}

func (a *clusterAPI) listSubjects(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	sr := a.srClient(w, cluster)
	if sr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	subs, err := sr.ListSubjectsWithVersions(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
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
	sr := a.srClient(w, cluster)
	if sr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	vs, err := sr.ListVersions(ctx, subject)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subject": subject, "versions": vs})
}

func (a *clusterAPI) getSchemaVersion(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	subject := chi.URLParam(r, "subject")
	version := chi.URLParam(r, "version")
	sr := a.srClient(w, cluster)
	if sr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	v, err := sr.GetVersion(ctx, subject, version)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *clusterAPI) registerSchema(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	subject := chi.URLParam(r, "subject")
	sr := a.srClient(w, cluster)
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
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *clusterAPI) deleteSubject(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	subject := chi.URLParam(r, "subject")
	sr := a.srClient(w, cluster)
	if sr == nil {
		return
	}
	permanent := r.URL.Query().Get("permanent") == "true"
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	versions, err := sr.DeleteSubject(ctx, subject, permanent)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": subject, "versions": versions, "permanent": permanent})
}

// alterTopicConfigs applies incremental config changes to a topic.
func (a *clusterAPI) alterTopicConfigs(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	topic := chi.URLParam(r, "topic")
	var req kafkapkg.AlterTopicConfigsRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, err := a.reg.AlterTopicConfigs(ctx, cluster, topic, req)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		msg := err.Error()
		status := http.StatusBadGateway
		if strings.Contains(msg, "required") || strings.Contains(msg, "no changes") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": "kafka: " + msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
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
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "kafka: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cluster": cluster, "acls": acls})
}

// searchMessages scans a topic for records matching a predicate.
//
// Body (application/json):
//
//	{
//	 "partition":   -1,
//	 "limit":       50,
//	 "budget":      10000,
//	 "direction":   "newest_first" | "oldest_first",
//	 "stop_on_limit": true,
//	 "mode":        "contains" | "jsonpath" | "xpath",
//	 "path":        "$.order.id" | "//order/@id" | "",
//	 "op":          "eq" | "ne" | "contains" | "regex" | "gt" | "lt" | "gte" | "lte" | "exists",
//	 "value":       "42",
//	 "zones":       ["value","key","headers"],
//	 "from_ts_ms":  1711234567890,
//	 "to_ts_ms":    1711239999999,
//	 "cursors":     {"0": 12340, "1": 12202}
//	}
func (a *clusterAPI) searchMessages(w http.ResponseWriter, r *http.Request) {
	cluster := chi.URLParam(r, "cluster")
	topic := chi.URLParam(r, "topic")

	opts, err := parseSearchBody(r)
	if err != nil {
		if writeParamError(w, err) {
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := a.reg.SearchMessages(ctx, cluster, topic, opts)
	if err != nil {
		if errors.Is(err, kafkapkg.ErrUnknownCluster) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown cluster: " + cluster})
			return
		}
		msg := err.Error()
		status := http.StatusBadGateway
		if strings.Contains(msg, "jsonpath") || strings.Contains(msg, "xpath") ||
			strings.Contains(msg, "regex") || strings.Contains(msg, "numeric op") ||
			strings.Contains(msg, "unknown search mode") || strings.Contains(msg, "unknown operator") ||
			strings.Contains(msg, "js filter") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster":  cluster,
		"topic":    topic,
		"messages": res.Messages,
		"search":   res.Stats,
	})
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
		status := http.StatusBadGateway
		if strings.Contains(msg, "required") || strings.Contains(msg, "validate") ||
			strings.Contains(msg, "resource_type") || strings.Contains(msg, "pattern_type") ||
			strings.Contains(msg, "operation") || strings.Contains(msg, "permission_type") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": msg})
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
		status := http.StatusBadGateway
		if strings.Contains(msg, "required") || strings.Contains(msg, "validate") ||
			strings.Contains(msg, "resource_type") || strings.Contains(msg, "pattern_type") ||
			strings.Contains(msg, "operation") || strings.Contains(msg, "permission_type") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": msg})
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
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "kafka: " + err.Error()})
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
		status := http.StatusBadGateway
		if strings.Contains(msg, "required") || strings.Contains(msg, "mechanism") ||
			strings.Contains(msg, "iterations") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": msg})
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
		status := http.StatusBadGateway
		if strings.Contains(msg, "required") || strings.Contains(msg, "mechanism") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": msg})
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
