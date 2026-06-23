// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/twmb/franz-go/pkg/kadm"
)

func TestClampToBounds(t *testing.T) {
	t.Parallel()

	starts := kadm.ListedOffsets{"t": {0: kadm.ListedOffset{Topic: "t", Partition: 0, Offset: 100}}}
	ends := kadm.ListedOffsets{"t": {0: kadm.ListedOffset{Topic: "t", Partition: 0, Offset: 200}}}

	assert.Equal(t, int64(150), clampToBounds(150, starts, ends, "t", 0), "in-range stays")
	assert.Equal(t, int64(100), clampToBounds(50, starts, ends, "t", 0), "below start clamps up")
	assert.Equal(t, int64(200), clampToBounds(999, starts, ends, "t", 0), "above end clamps down")

	// Error entries must be ignored (treated as no bound on that side).
	errEnds := kadm.ListedOffsets{"t": {0: kadm.ListedOffset{Topic: "t", Partition: 0, Offset: 200, Err: errors.New("boom")}}}
	assert.Equal(t, int64(999), clampToBounds(999, starts, errEnds, "t", 0), "errored end bound is ignored")
}
