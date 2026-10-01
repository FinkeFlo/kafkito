// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// The decoded query parameter selects the form of the value, and a decoded
// value names its Schema Registry format in a response header.
func TestDownloadMessageRaw_DecodedParam(t *testing.T) {
	t.Parallel()

	const path = "/api/v1/clusters/test/topics/users/messages/0/3/raw"
	decoded := &kafkapkg.RawMessageValue{Value: []byte(`{"id":1}`), ContentType: "application/json", Extension: "json", DecodedFormat: "avro"}
	wire := &kafkapkg.RawMessageValue{Value: []byte{0x00, 0x00, 0x00, 0x00, 0x07, 0x02}, ContentType: "application/octet-stream", Extension: "bin"}

	for _, tc := range []struct {
		name        string
		query       string
		wantStatus  int
		wantOpts    kafkapkg.RawValueOptions
		wantFile    string
		wantDecoded string
	}{
		{name: "decoded by default", wantStatus: 200, wantFile: "users-p0-o3.json", wantDecoded: "avro"},
		{name: "decoded=true", query: "?decoded=true", wantStatus: 200, wantFile: "users-p0-o3.json", wantDecoded: "avro"},
		{name: "decoded=false", query: "?decoded=false", wantStatus: 200, wantOpts: kafkapkg.RawValueOptions{WireBytes: true}, wantFile: "users-p0-o3.bin"},
		{name: "decoded=0 is rejected", query: "?decoded=0", wantStatus: 400},
		{name: "decoded=TRUE is rejected", query: "?decoded=TRUE", wantStatus: 400},
		{name: "decoded=yes is rejected", query: "?decoded=yes", wantStatus: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fetched := 0
			h := fakeServer(t, stores{messages: fakeMessages{raw: func(topic string, partition int32, offset int64, opts kafkapkg.RawValueOptions) (*kafkapkg.RawMessageValue, error) {
				fetched++
				assert.Equal(t, "users", topic)
				assert.EqualValues(t, 0, partition)
				assert.EqualValues(t, 3, offset)
				assert.Equal(t, tc.wantOpts, opts)
				if opts.WireBytes {
					return wire, nil
				}
				return decoded, nil
			}}})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+tc.query, nil))

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			if tc.wantStatus != http.StatusOK {
				assert.Contains(t, rec.Body.String(), `"code":"invalid_request"`)
				assert.Contains(t, rec.Body.String(), `parameter \"decoded\" in query`)
				assert.Zero(t, fetched, "a rejected request must not read the record")
				return
			}
			assert.Equal(t, `attachment; filename="`+tc.wantFile+`"`, rec.Header().Get("Content-Disposition"))
			assert.Equal(t, tc.wantDecoded, rec.Header().Get("X-Kafkito-Value-Decoded"))
			if tc.wantDecoded != "" {
				assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
				assert.Equal(t, `{"id":1}`, rec.Body.String())
			} else {
				assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
				assert.Equal(t, wire.Value, rec.Body.Bytes())
			}
		})
	}
}
