// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/twmb/franz-go/pkg/kadm"
)

func TestLogEndOffset(t *testing.T) {
	t.Parallel()

	ends := kadm.ListedOffsets{
		"orders": {
			0: kadm.ListedOffset{Topic: "orders", Partition: 0, Offset: 42},
			1: kadm.ListedOffset{Topic: "orders", Partition: 1, Offset: 99, Err: errors.New("leader unavailable")},
			2: kadm.ListedOffset{Topic: "orders", Partition: 2, Offset: -1},
		},
	}

	assert.Equal(t, int64(42), logEndOffset(ends, "orders", 0), "healthy partition returns its offset")
	assert.Equal(t, int64(-1), logEndOffset(ends, "orders", 1), "partition with an error must not return its sentinel offset")
	assert.Equal(t, int64(-1), logEndOffset(ends, "orders", 2), "negative offset must be treated as unknown")
	assert.Equal(t, int64(-1), logEndOffset(ends, "orders", 9), "missing partition is unknown")
	assert.Equal(t, int64(-1), logEndOffset(ends, "other", 0), "missing topic is unknown")
}
