// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package api embeds the OpenAPI 3.1 document that is the single source of
// truth for kafkito's HTTP contract (see docs/adr/0005-openapi-contract.md).
package api

import _ "embed"

// Spec is the raw OpenAPI document (api/openapi.yaml).
//
//go:embed openapi.yaml
var Spec []byte
