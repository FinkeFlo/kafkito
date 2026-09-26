// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	gen "github.com/FinkeFlo/kafkito/internal/server/api"
)

// generatedRoutes registers every operation of the generated strict server
// (api/openapi.yaml) on the chi groups of server.New.
//
// The generated HandlerWithOptions would put every operation on a single
// router. Instead, each operation's generated wrapper method is mounted
// individually on the group whose middleware chain it needs (none, auth, or
// auth + private cluster + RBAC), with a relative path so chi reports the
// route pattern that RBAC resolves permissions from.
//
// Every route runs, after the group middleware:
//
//	body limit → OpenAPI request validator → parameter binding → handler
//
// The body limit comes first because the validator buffers the whole body.
type generatedRoutes struct {
	w        *gen.ServerInterfaceWrapper
	validate func(http.Handler) http.Handler
	errs     errorWriter
}

// extra strict middlewares run inside withHTTPRequest (tests use them to
// observe the bound request objects).
func newGeneratedRoutes(impl gen.StrictServerInterface, errs errorWriter, extra ...gen.StrictMiddlewareFunc) (*generatedRoutes, error) {
	validate, err := newRequestValidator(errs)
	if err != nil {
		return nil, err
	}
	strict := gen.NewStrictHandlerWithOptions(impl, append(extra, withHTTPRequest), gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			var ae *apiError
			if !errors.As(err, &ae) {
				err = &apiError{Status: http.StatusBadRequest, Code: invalidRequestCode, Message: "invalid body: malformed JSON", Err: err}
			}
			errs.writeError(w, r, err)
		},
		ResponseErrorHandlerFunc: errs.writeError,
	})
	return &generatedRoutes{
		w:        &gen.ServerInterfaceWrapper{Handler: strict, ErrorHandlerFunc: errs.writeError},
		validate: validate,
		errs:     errs,
	}, nil
}

// mountRoot registers the unauthenticated probes on the root router.
func (g *generatedRoutes) mountRoot(r chi.Router) {
	r.With(noRequestBody, g.validate).Get("/healthz", g.w.GetHealth)
	r.With(noRequestBody, g.validate).Get("/readyz", g.w.GetReadiness)
}

// mountMeta registers the authenticated meta endpoints under /api/v1.
func (g *generatedRoutes) mountMeta(r chi.Router) {
	r.With(noRequestBody, g.validate).Get("/info", g.w.GetInfo)
	r.With(noRequestBody, g.validate).Get("/me", g.w.GetMe)
	r.With(noRequestBody, g.validate).Get("/openapi.yaml", g.w.GetOpenApiSpec)
}

// mountClusters registers the cluster endpoints on the /api/v1 group that
// runs the private-cluster, RBAC and private-cluster-param middleware.
func (g *generatedRoutes) mountClusters(r chi.Router) {
	noBody := r.With(noRequestBody, g.validate)
	noBody.Get("/clusters", g.w.ListClusters)
	r.With(limitRequestBody(maxPrivateClusterHeaderBytes), g.validate).
		Post("/clusters/_test", g.w.TestCluster)
	noBody.Get("/clusters/{cluster}/capabilities", g.w.GetCapabilities)
	noBody.Post("/clusters/{cluster}/capabilities/refresh", g.w.RefreshCapabilities)
	noBody.Get("/clusters/{cluster}/brokers", g.w.ListBrokers)

	jsonBody := r.With(limitRequestBody(maxJSONBodyBytes), g.validate)
	noBody.Get("/clusters/{cluster}/topics", g.w.ListTopics)
	jsonBody.Post("/clusters/{cluster}/topics", g.w.CreateTopic)
	noBody.Get("/clusters/{cluster}/topics/{topic}", g.w.DescribeTopic)
	noBody.Delete("/clusters/{cluster}/topics/{topic}", g.w.DeleteTopic)
	noBody.Get("/clusters/{cluster}/topics/{topic}/consumers", g.w.ListTopicConsumers)
	jsonBody.Patch("/clusters/{cluster}/topics/{topic}/configs", g.w.AlterTopicConfigs)
	jsonBody.Delete("/clusters/{cluster}/topics/{topic}/records", g.w.DeleteRecords)

	noBody.Get("/clusters/{cluster}/topics/{topic}/messages", g.w.ConsumeMessages)
	r.With(produceBody(g.errs), g.validate).
		Post("/clusters/{cluster}/topics/{topic}/messages", g.w.ProduceMessage)
	noBody.Get("/clusters/{cluster}/topics/{topic}/messages/count", g.w.CountMessages)
	noBody.Get("/clusters/{cluster}/topics/{topic}/messages/timeline", g.w.GetMessageTimeline)
	noBody.Get("/clusters/{cluster}/topics/{topic}/messages/{partition}/{offset}/raw", g.w.DownloadMessageRaw)
	noBody.Get("/clusters/{cluster}/topics/{topic}/sample", g.w.SampleMessages)
	r.With(limitRequestBodyMsg(maxSearchBodyBytes, "invalid json body: "), g.validate).
		Post("/clusters/{cluster}/topics/{topic}/messages/search", g.w.SearchMessages)
	r.With(limitRequestBody(maxCopyBodyBytes), g.validate).
		Post("/clusters/{cluster}/topics/{topic}/copy", g.w.CopyMessages)

	groupBody := r.With(limitRequestBody(maxGroupBodyBytes), g.validate)
	noBody.Get("/clusters/{cluster}/groups", g.w.ListGroups)
	groupBody.Post("/clusters/{cluster}/groups", g.w.CreateGroup)
	noBody.Get("/clusters/{cluster}/groups/{group}", g.w.DescribeGroup)
	noBody.Delete("/clusters/{cluster}/groups/{group}", g.w.DeleteGroup)
	groupBody.Post("/clusters/{cluster}/groups/{group}/reset-offsets", g.w.ResetGroupOffsets)

	noBody.Get("/clusters/{cluster}/schemas/subjects", g.w.ListSubjects)
	noBody.Delete("/clusters/{cluster}/schemas/subjects/{subject}", g.w.DeleteSubject)
	noBody.Get("/clusters/{cluster}/schemas/subjects/{subject}/versions", g.w.ListSchemaVersions)
	r.With(limitRequestBody(maxRegisterSchemaBodyBytes), g.validate).
		Post("/clusters/{cluster}/schemas/subjects/{subject}/versions", g.w.RegisterSchema)
	noBody.Get("/clusters/{cluster}/schemas/subjects/{subject}/versions/{version}", g.w.GetSchemaVersion)

	// The ACL and SCRAM limits predate the shared message prefix.
	aclBody := r.With(limitRequestBodyMsg(maxACLBodyBytes, "invalid json: "), g.validate)
	noBody.Get("/clusters/{cluster}/acls", g.w.ListAcls)
	aclBody.Post("/clusters/{cluster}/acls", g.w.CreateAcl)
	aclBody.Delete("/clusters/{cluster}/acls", g.w.DeleteAcl)

	noBody.Get("/clusters/{cluster}/users", g.w.ListScramUsers)
	r.With(limitRequestBodyMsg(maxSCRAMBodyBytes, "invalid json: "), g.validate).
		Post("/clusters/{cluster}/users", g.w.UpsertScramUser)
	noBody.Delete("/clusters/{cluster}/users/{user}", g.w.DeleteScramUser)
}
