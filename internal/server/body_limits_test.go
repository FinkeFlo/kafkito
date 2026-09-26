package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// padJSON pads the JSON object obj with spaces to exactly n bytes.
func padJSON(t *testing.T, obj string, n int) string {
	t.Helper()
	require.LessOrEqual(t, len(obj), n)
	s := obj[:len(obj)-1] + strings.Repeat(" ", n-len(obj)) + "}"
	require.Len(t, s, n)
	return s
}

func sendBody(h http.Handler, method, path string, body io.Reader, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// Each body-carrying operation caps its body before the request validator
// buffers it: a body at the limit is served, one byte more is rejected with
// the operation's limit error, and an endless body is not read much past the
// limit.
func TestTopicMessageOps_BodyLimits(t *testing.T) {
	t.Parallel()
	h := kfakeServer(t, "orders")
	const topic = "/api/v1/clusters/kf/topics/orders"
	tooLarge := `{"error":"invalid body: http: request body too large"}`

	for _, tc := range []struct {
		name, method, path, obj string
		limit                   int
		wantOK                  int
		wantTooLarge            string
	}{
		{"createTopic", http.MethodPost, "/api/v1/clusters/kf/topics", `{"name":"limit-topic"}`, maxJSONBodyBytes, http.StatusCreated, tooLarge},
		{"alterTopicConfigs", http.MethodPatch, topic + "/configs", `{"set":{"retention.ms":"1000"}}`, maxJSONBodyBytes, http.StatusOK, tooLarge},
		{"deleteRecords", http.MethodDelete, topic + "/records", `{"partitions":{"0":0}}`, maxJSONBodyBytes, http.StatusOK, tooLarge},
		{"searchMessages", http.MethodPost, topic + "/messages/search", `{"limit":1,"budget":1}`, maxSearchBodyBytes, http.StatusOK, `{"error":"invalid json body: http: request body too large"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := sendBody(h, tc.method, tc.path, strings.NewReader(padJSON(t, tc.obj, tc.limit)), nil)
			assert.Equal(t, tc.wantOK, rec.Code, "at the limit: %s", rec.Body.String())

			rec = sendBody(h, tc.method, tc.path, strings.NewReader(padJSON(t, tc.obj, tc.limit+1)), nil)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.JSONEq(t, tc.wantTooLarge, rec.Body.String())

			src := &countingReader{}
			rec = sendBody(h, tc.method, tc.path, src, nil)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.LessOrEqual(t, src.n, int64(tc.limit)+64<<10, "read %d bytes", src.n)
		})
	}
}

// The copy body limit (32 KiB) is checked with a fake registry, so the copy
// at the limit completes without a broker.
func TestCopyMessages_BodyLimit(t *testing.T) {
	holdCopySlots(t, maxConcurrentCopies)
	h, fake := newCopyStreamHandler(t)
	fake.unblockNow()
	const path = "/api/v1/clusters/src/topics/orders/copy"

	rec := sendBody(h, http.MethodPost, path, strings.NewReader(padJSON(t, copyStreamBody, maxCopyBodyBytes)), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"done":true`)

	rec = sendBody(h, http.MethodPost, path, strings.NewReader(padJSON(t, copyStreamBody, maxCopyBodyBytes+1)), nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t, `{"error":"invalid body: http: request body too large"}`, rec.Body.String())

	src := &countingReader{}
	rec = sendBody(h, http.MethodPost, path, src, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.LessOrEqual(t, src.n, int64(maxCopyBodyBytes)+64<<10, "read %d bytes", src.n)
	assert.Eventually(t, func() bool { return len(copySlots) == 0 }, 5*time.Second, 10*time.Millisecond)
}

func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(b)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// The produce limit is 15 MiB of decompressed JSON, answered with 413 and
// its own message, for plain and gzip bodies alike; the compressed wire
// bytes are capped separately.
func TestProduceMessage_BodyLimits(t *testing.T) {
	t.Parallel()
	h := kfakeServer(t, "orders")
	const path = "/api/v1/clusters/kf/topics/orders/messages"
	gz := map[string]string{"Content-Encoding": "gzip"}
	limitMsg := `{"error":"request body exceeds the 15 MB produce limit"}`
	atLimit := padJSON(t, `{"value":"v"}`, maxProduceBodyBytes)
	overLimit := padJSON(t, `{"value":"v"}`, maxProduceBodyBytes+1)

	t.Run("plain at the limit", func(t *testing.T) {
		t.Parallel()
		rec := sendBody(h, http.MethodPost, path, strings.NewReader(atLimit), nil)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})
	t.Run("plain over the limit", func(t *testing.T) {
		t.Parallel()
		rec := sendBody(h, http.MethodPost, path, strings.NewReader(overLimit), nil)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.JSONEq(t, limitMsg, rec.Body.String())
	})
	t.Run("gzip at the decompressed limit", func(t *testing.T) {
		t.Parallel()
		rec := sendBody(h, http.MethodPost, path, bytes.NewReader(gzipBytes(t, []byte(atLimit))), gz)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})
	t.Run("gzip bomb over the decompressed limit", func(t *testing.T) {
		t.Parallel()
		body := gzipBytes(t, []byte(overLimit))
		require.Less(t, len(body), 1<<20, "the compressed body is small")
		rec := sendBody(h, http.MethodPost, path, bytes.NewReader(body), gz)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.JSONEq(t, limitMsg, rec.Body.String())
	})
	t.Run("gzip over the wire limit", func(t *testing.T) {
		t.Parallel()
		// A gzip header followed by endless empty deflate blocks
		// decompresses to nothing: only the wire cap ends the read.
		src := &emptyDeflateBlocks{}
		rec := sendBody(h, http.MethodPost, path, io.MultiReader(bytes.NewReader(gzipBytes(t, nil)[:10]), src), gz)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.JSONEq(t, limitMsg, rec.Body.String())
		assert.Greater(t, src.n, int64(maxProduceBodyBytes), "the decompressed cap did not end the read")
		assert.LessOrEqual(t, src.n, int64(maxProduceCompressedBodyBytes)+64<<10, "read %d bytes", src.n)
	})
	t.Run("endless plain body", func(t *testing.T) {
		t.Parallel()
		src := &countingReader{}
		rec := sendBody(h, http.MethodPost, path, src, nil)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.JSONEq(t, limitMsg, rec.Body.String())
		assert.LessOrEqual(t, src.n, int64(maxProduceCompressedBodyBytes)+64<<10, "read %d bytes", src.n)
	})
	t.Run("invalid gzip", func(t *testing.T) {
		t.Parallel()
		rec := sendBody(h, http.MethodPost, path, strings.NewReader(`{"value":"v"}`), gz)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.JSONEq(t, `{"error":"invalid gzip body: gzip: invalid header"}`, rec.Body.String())
	})
}

// With RBAC on, the middleware reads the create-topic body to find the topic
// name before the route's limit applies. That read is capped too, and the
// body it read still reaches the validator and the handler.
func TestCreateTopic_RBACBodyRead(t *testing.T) {
	t.Parallel()
	addr := startKfake(t)
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "kf", Brokers: []string{addr}}}, slog.Default())
	t.Cleanup(reg.Close)
	h := New(Options{Version: "test", Logger: slog.Default(), Registry: reg, Config: config.Config{RBAC: config.RBACConfig{
		Enabled:  true,
		Identity: config.IdentityConfig{Header: rbacTestHeader},
		Roles:    []config.RoleConfig{{Name: "creator", Permissions: []config.PermissionConfig{{Resource: "topic:allowed-*", Actions: []string{"edit"}}}}},
		Subjects: []config.SubjectConfig{{User: userMallory, Roles: []string{"creator"}}},
	}}})
	user := map[string]string{rbacTestHeader: userMallory}
	const path = "/api/v1/clusters/kf/topics"

	rec := sendBody(h, http.MethodPost, path, strings.NewReader(padJSON(t, `{"name":"allowed-a"}`, maxJSONBodyBytes)), user)
	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"created":"allowed-a"}`, rec.Body.String())

	rec = sendBody(h, http.MethodPost, path, strings.NewReader(`{"name":"allowed-b","bogus":1}`), user)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "the validator sees the body the middleware read")
	assert.Contains(t, rec.Body.String(), `"code":"invalid_request"`)

	rec = sendBody(h, http.MethodPost, path, strings.NewReader(`{"name":"denied"}`), user)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	rec = sendBody(h, http.MethodPost, path, strings.NewReader(padJSON(t, `{"name":"allowed-c"}`, maxJSONBodyBytes+1)), user)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t, `{"error":"invalid body: http: request body too large"}`, rec.Body.String())

	src := &countingReader{}
	rec = sendBody(h, http.MethodPost, path, src, user)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.LessOrEqual(t, src.n, int64(maxJSONBodyBytes)+64<<10, "read %d bytes", src.n)
}

// emptyDeflateBlocks yields endless empty, non-final stored deflate blocks
// and counts the bytes read.
type emptyDeflateBlocks struct{ n int64 }

func (e *emptyDeflateBlocks) Read(p []byte) (int, error) {
	block := [5]byte{0x00, 0x00, 0x00, 0xff, 0xff}
	for i := range p {
		p[i] = block[(e.n+int64(i))%int64(len(block))]
	}
	e.n += int64(len(p))
	return len(p), nil
}
