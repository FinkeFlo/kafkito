package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/netguard"
)

// The tests in this file drive the copy SSE stream with a fake registry. They
// are not parallel: the copy semaphore is package-level and they assert on
// its occupancy.

// blockingCopyRegistry serves one page with a single record, then blocks the
// next ConsumeMessages call until release is closed or the job's context is
// done.
type blockingCopyRegistry struct {
	reg     *kafkapkg.Registry
	blocked chan struct{} // closed when the second consume call blocks
	release chan struct{} // close to let the second consume call finish

	calls     atomic.Int32
	cancelled atomic.Bool // the blocked call saw its context end
	produced  atomic.Int64
	once      sync.Once
	unblock   sync.Once
}

// unblockNow closes release once; later calls are no-ops.
func (f *blockingCopyRegistry) unblockNow() { f.unblock.Do(func() { close(f.release) }) }

func newBlockingCopyRegistry(reg *kafkapkg.Registry) *blockingCopyRegistry {
	return &blockingCopyRegistry{reg: reg, blocked: make(chan struct{}), release: make(chan struct{})}
}

func (f *blockingCopyRegistry) ConfigFor(name string) (config.ClusterConfig, bool) {
	return f.reg.ConfigFor(name)
}

func (f *blockingCopyRegistry) UseAdhoc(cfg config.ClusterConfig) (string, error) {
	return f.reg.UseAdhoc(cfg)
}

func (f *blockingCopyRegistry) DescribeTopic(context.Context, string, string) (*kafkapkg.TopicDetail, error) {
	return &kafkapkg.TopicDetail{Partitions: []kafkapkg.PartitionInfo{{Partition: 0}}}, nil
}

func (f *blockingCopyRegistry) ConsumeMessages(ctx context.Context, _, _ string, _ kafkapkg.ConsumeOptions) (*kafkapkg.ConsumeResult, error) {
	if f.calls.Add(1) == 1 {
		return &kafkapkg.ConsumeResult{
			Messages:   []kafkapkg.Message{{Partition: 0, Offset: 0, Timestamp: 1, Value: "v", ValueEncoding: "text"}},
			HasMore:    true,
			NextCursor: &kafkapkg.Cursor{Partitions: map[int32]int64{0: 1}, Direction: kafkapkg.CursorForward},
		}, nil
	}
	f.once.Do(func() { close(f.blocked) })
	select {
	case <-f.release:
		return &kafkapkg.ConsumeResult{}, nil
	case <-ctx.Done():
		f.cancelled.Store(true)
		return nil, ctx.Err()
	}
}

func (f *blockingCopyRegistry) ProduceBatch(_ context.Context, _, _ string, reqs []kafkapkg.ProduceRequest) (int, error) {
	f.produced.Add(int64(len(reqs)))
	return len(reqs), nil
}

// newCopyStreamHandler serves server.New with the fake as copy registry and
// the clusters src and dst.
func newCopyStreamHandler(t *testing.T) (http.Handler, *blockingCopyRegistry) {
	t.Helper()
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{
		{Name: "src", Brokers: []string{"127.0.0.1:19092"}},
		{Name: "dst", Brokers: []string{"127.0.0.1:19093"}},
	}, slog.Default())
	t.Cleanup(reg.Close)
	fake := newBlockingCopyRegistry(reg)
	h := New(Options{Version: "test", Logger: slog.Default(), Registry: reg, Config: config.Defaults(), copyRegistry: fake})
	return h, fake
}

const copyStreamBody = `{"dest_cluster":"dst","dest_topic":"orders2"}`

// holdCopySlots takes all but free copy slots for the duration of the test.
func holdCopySlots(t *testing.T, free int) int {
	t.Helper()
	require.Empty(t, copySlots, "copy semaphore should start empty")
	held := 0
	for held < maxConcurrentCopies-free && tryAcquireCopySlot() {
		held++
	}
	t.Cleanup(func() {
		for range held {
			releaseCopySlot()
		}
	})
	return held
}

// readEvent reads one "data: ...\n\n" event off the stream.
func readEvent(t *testing.T, r *bufio.Reader) copyProgressEvent {
	t.Helper()
	line, err := r.ReadString('\n')
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(line, "data: "), "unexpected line %q", line)
	blank, err := r.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "\n", blank)
	var ev copyProgressEvent
	require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev))
	return ev
}

// Events reach the client while the job is still running: the fake holds
// the job in its second page until the client has seen the first page's
// progress, so a buffered response would deadlock and time out.
func TestCopyStream_EventsArriveIncrementally(t *testing.T) {
	holdCopySlots(t, maxConcurrentCopies)
	h, fake := newCopyStreamHandler(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	// Runs before srv.Close, which waits for the handler, so a failing
	// assertion cannot hang the test.
	t.Cleanup(fake.unblockNow)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/v1/clusters/src/topics/orders/copy", strings.NewReader(copyStreamBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "no", resp.Header.Get("X-Accel-Buffering"))
	assert.Empty(t, resp.Header.Get("Content-Length"))
	assert.Equal(t, int64(-1), resp.ContentLength, "the stream has no length")

	r := bufio.NewReader(resp.Body)
	assert.Equal(t, copyProgressEvent{}, readEvent(t, r))
	assert.Equal(t, copyProgressEvent{Copied: 1}, readEvent(t, r))
	select {
	case <-fake.blocked:
	case <-ctx.Done():
		t.Fatal("job never reached its second page")
	}
	assert.Len(t, copySlots, 1, "the running job holds its slot")

	fake.unblockNow()
	// The empty page is retried before the job concludes it is drained.
	var last copyProgressEvent
	for !last.Done {
		last = readEvent(t, r)
	}
	assert.Equal(t, copyProgressEvent{Copied: 1, Done: true}, last)
	_, err = r.ReadByte()
	require.ErrorIs(t, err, io.EOF, "the stream ends after the done event")
	assert.Eventually(t, func() bool { return len(copySlots) == 0 }, 5*time.Second, 10*time.Millisecond, "slot released")
}

// A client that goes away mid-copy stops the job: the goroutine ends and its
// concurrency slot is free for the next request, even when it was the last
// free one.
func TestCopyStream_ClientAbortStopsJobAndFreesSlot(t *testing.T) {
	held := holdCopySlots(t, 1)
	h, fake := newCopyStreamHandler(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	// Runs before srv.Close, which waits for the handler, so a failing
	// assertion cannot hang the test.
	t.Cleanup(fake.unblockNow)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/v1/clusters/src/topics/orders/copy", strings.NewReader(copyStreamBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	r := bufio.NewReader(resp.Body)
	readEvent(t, r)
	readEvent(t, r)
	<-fake.blocked
	require.Len(t, copySlots, maxConcurrentCopies, "all slots are taken while the job runs")

	// While the job runs, a second copy is shed.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCopyRequest("/api/v1/clusters/src/topics/orders/copy", copyStreamBody))
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())

	cancel()
	_ = resp.Body.Close()

	assert.Eventually(t, fake.cancelled.Load, 5*time.Second, 10*time.Millisecond, "the job's context is cancelled")
	// releaseCopySlot is the goroutine's last deferred call, so the slot
	// count dropping proves the goroutine has ended.
	assert.Eventually(t, func() bool { return len(copySlots) == held }, 5*time.Second, 10*time.Millisecond, "slot released")

	// The freed slot is usable: the next copy streams instead of a 429.
	next, fake2 := newCopyStreamHandler(t)
	fake2.unblockNow()
	rec = httptest.NewRecorder()
	next.ServeHTTP(rec, newCopyRequest("/api/v1/clusters/src/topics/orders/copy", copyStreamBody))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"done":true`)
	assert.Equal(t, int64(2), fake2.produced.Load()+fake.produced.Load())
}

// failingWriter accepts the first write and fails every later one, like a
// connection that dropped after the stream opened.
type failingWriter struct {
	*httptest.ResponseRecorder
	writes int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, errors.New("connection reset")
	}
	return w.ResponseRecorder.Write(p)
}

// After the 30 s request deadline has fired, a disconnect no longer cancels
// the request context; it only shows as a failed write. The response writer
// then closes the stream body, which must stop the job as well.
func TestCopyStream_FailedWriteAfterDeadlineStopsJob(t *testing.T) {
	holdCopySlots(t, maxConcurrentCopies)
	h, fake := newCopyStreamHandler(t)

	req := newCopyRequest("/api/v1/clusters/src/topics/orders/copy", copyStreamBody)
	ctx, cancel := context.WithTimeout(req.Context(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	w := &failingWriter{ResponseRecorder: httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(w, req.WithContext(ctx))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handler did not return after the failed write")
	}
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "data: {\"copied\":0}\n\n", w.Body.String())
	assert.Eventually(t, func() bool { return len(copySlots) == 0 }, 5*time.Second, 10*time.Millisecond, "slot released")
	assert.LessOrEqual(t, fake.calls.Load(), int32(2), "the job stopped instead of copying on")
}

// The request deadline (middleware.Timeout, 30 s in production) does not
// end a copy: the job outlives it and completes.
func TestCopyStream_OutlivesRequestDeadline(t *testing.T) {
	holdCopySlots(t, maxConcurrentCopies)
	h, fake := newCopyStreamHandler(t)

	req := newCopyRequest("/api/v1/clusters/src/topics/orders/copy", copyStreamBody)
	ctx, cancel := context.WithTimeout(req.Context(), 200*time.Millisecond)
	defer cancel()
	go func() {
		<-fake.blocked
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		fake.unblockNow()
	}()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(ctx))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, fake.cancelled.Load(), "the deadline cancelled the job")
	assert.True(t, strings.HasSuffix(rec.Body.String(), "data: {\"copied\":1,\"done\":true}\n\n"), rec.Body.String())
	assert.Eventually(t, func() bool { return len(copySlots) == 0 }, 5*time.Second, 10*time.Millisecond, "slot released")
}

// Every event of the stream matches the CopyProgressEvent schema of the spec.
func TestCopyStream_EventsMatchSpec(t *testing.T) {
	holdCopySlots(t, maxConcurrentCopies)
	h, fake := newCopyStreamHandler(t)
	fake.unblockNow()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCopyRequest("/api/v1/clusters/src/topics/orders/copy", copyStreamBody))
	require.Equal(t, http.StatusOK, rec.Code)

	doc, err := loadSpec()
	require.NoError(t, err)
	schema := doc.Components.Schemas["CopyProgressEvent"].Value
	events := strings.Split(strings.TrimSuffix(rec.Body.String(), "\n\n"), "\n\n")
	require.GreaterOrEqual(t, len(events), 3)
	for _, ev := range events {
		require.True(t, strings.HasPrefix(ev, "data: "), ev)
		var v any
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(ev, "data: ")), &v))
		assert.NoError(t, schema.VisitJSON(v, openapi3.MultiErrors()), ev)
	}
}

// failingCopyRegistry is a blockingCopyRegistry whose consume or produce
// call fails with the given error.
type failingCopyRegistry struct {
	*blockingCopyRegistry
	consumeErr error
	produceErr error
}

func (f *failingCopyRegistry) ConsumeMessages(ctx context.Context, cluster, topic string, opts kafkapkg.ConsumeOptions) (*kafkapkg.ConsumeResult, error) {
	if f.consumeErr != nil {
		return nil, f.consumeErr
	}
	return f.blockingCopyRegistry.ConsumeMessages(ctx, cluster, topic, opts)
}

func (f *failingCopyRegistry) ProduceBatch(ctx context.Context, cluster, topic string, reqs []kafkapkg.ProduceRequest) (int, error) {
	if f.produceErr != nil {
		return 0, f.produceErr
	}
	return f.blockingCopyRegistry.ProduceBatch(ctx, cluster, topic, reqs)
}

// refusedDialErr is a refused dial as franz-go reports it, naming the
// broker address.
func refusedDialErr() error {
	return fmt.Errorf("unable to dial: %w", &net.OpError{
		Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 7), Port: 9092},
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
	})
}

// Issue #126: a copy job that hits an address the outbound guard refused
// reports the same static message as the 502, not the refused host, which
// may come from the X-Kafkito-Cluster header. Other errors get a text
// without address either (see copyErrorText). The operator WARN carries the
// full error.
func TestCopyStream_ErrorEventHidesAddresses(t *testing.T) {
	blocked := fmt.Errorf("unable to dial: %w", &netguard.BlockedAddressError{
		Addr: "rebind.example:9092", Host: "rebind.example", IP: netip.MustParseAddr("169.254.169.254"),
	})
	refused := refusedDialErr()
	notLeader := fmt.Errorf("fetch topic %q partition 0 on cluster %q: %w", "orders", "src", kerr.NotLeaderForPartition)
	cases := []struct {
		name      string
		consume   error
		produce   error
		wantError string
		// wantLog is the operator WARN; wantLogCluster the cluster it names.
		wantLog, wantLogCluster string
	}{
		{"consume blocked", blocked, nil, "consume: " + privateClusterAddressBlockedMsg, "copy: blocked address", "src"},
		{"produce blocked", nil, blocked, "produce: " + privateClusterAddressBlockedMsg, "copy: blocked address", "dst"},
		{"consume refused", refused, nil, "consume: connection refused", "copy: consume failed", "src"},
		{"produce refused", nil, fmt.Errorf("produce: %w", refused), "produce: connection refused", "copy: produce failed", "dst"},
		{"consume kafka error", notLeader, nil, "consume: " + kerr.NotLeaderForPartition.Error(), "copy: consume failed", "src"},
		{"produce kafka error", nil, fmt.Errorf("produce: %w", kerr.TopicAuthorizationFailed), "produce: " + kerr.TopicAuthorizationFailed.Error(), "copy: produce failed", "dst"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			holdCopySlots(t, maxConcurrentCopies)
			reg := kafkapkg.NewRegistry([]config.ClusterConfig{
				{Name: "src", Brokers: []string{"127.0.0.1:19092"}},
				{Name: "dst", Brokers: []string{"127.0.0.1:19093"}},
			}, slog.Default())
			t.Cleanup(reg.Close)
			base := newBlockingCopyRegistry(reg)
			base.unblockNow()
			fake := &failingCopyRegistry{blockingCopyRegistry: base, consumeErr: tc.consume, produceErr: tc.produce}
			logs := &syncBuffer{}
			logger := slog.New(slog.NewJSONHandler(logs, nil))
			h := New(Options{Version: "test", Logger: logger, Registry: reg, Config: config.Defaults(), copyRegistry: fake})

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, newCopyRequest("/api/v1/clusters/src/topics/orders/copy", copyStreamBody))
			require.Equal(t, http.StatusOK, rec.Code)

			events := strings.Split(strings.TrimSuffix(rec.Body.String(), "\n\n"), "\n\n")
			var last copyProgressEvent
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(events[len(events)-1], "data: ")), &last))
			assert.True(t, last.Done)
			assert.Equal(t, tc.wantError, last.Error)
			for _, raw := range []string{"rebind.example", "169.254.169.254", "10.0.0.7", "9092", "dial", "connect:"} {
				assert.NotContains(t, rec.Body.String(), raw)
			}

			// Operators still get the full cause: the #118 log policy allows
			// hosts and resolved IPs in logs, only the SSE event stays generic.
			var warns []map[string]any
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				var entry map[string]any
				if json.Unmarshal([]byte(line), &entry) == nil && strings.HasPrefix(fmt.Sprint(entry["msg"]), "copy: ") {
					warns = append(warns, entry)
				}
			}
			require.Len(t, warns, 1, "one copy WARN; logs:\n%s", logs.String())
			warn := warns[0]
			assert.Equal(t, tc.wantLog, warn["msg"])
			assert.Equal(t, "WARN", warn["level"])
			assert.Equal(t, tc.wantLogCluster, warn["cluster"])
			wantErr, wantTopic := tc.consume, "orders"
			if wantErr == nil {
				wantErr, wantTopic = tc.produce, "orders2"
			}
			assert.Equal(t, wantTopic, warn["topic"])
			assert.Equal(t, wantErr.Error(), warn["err"])
			// The job releases its slot after closing the stream, so the
			// next case waits for it.
			assert.Eventually(t, func() bool { return len(copySlots) == 0 }, 5*time.Second, 10*time.Millisecond, "slot released")
		})
	}
}

// copyErrorText keeps what helps the user and names no address, port or
// operating system error.
func TestCopyErrorText(t *testing.T) {
	t.Parallel()

	refused := refusedDialErr()
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"blocked", blockedDialErr(), privateClusterAddressBlockedMsg},
		{"unknown cluster", fmt.Errorf("%w: __private__", kafkapkg.ErrUnknownCluster), kafkapkg.ErrUnknownCluster.Error()},
		{"topic sentinel", fmt.Errorf("describe: %w", kafkapkg.ErrTopicNotFound), kafkapkg.ErrTopicNotFound.Error()},
		{"cancelled", fmt.Errorf("produce: %w", context.Canceled), "cancelled"},
		{"client closed", fmt.Errorf("produce: %w", kgo.ErrClientClosed), "cancelled"},
		{"not authorized", fmt.Errorf("produce: %w", kerr.TopicAuthorizationFailed), kerr.TopicAuthorizationFailed.Error()},
		{"record too large", fmt.Errorf("produce: %w", kerr.MessageTooLarge), kerr.MessageTooLarge.Error()},
		{"destination topic missing", fmt.Errorf("produce: %w", kerr.UnknownTopicOrPartition), kerr.UnknownTopicOrPartition.Error()},
		{"not confirmed in time", fmt.Errorf("%w, last err: %w", kgo.ErrRecordTimeout, kerr.NotEnoughReplicas), kerr.NotEnoughReplicas.Error()},
		{"source topic missing", errors.New(`topic "orders" not found on cluster "src"`), kafkapkg.ErrTopicNotFound.Error()},
		{"partition missing", errors.New(`partition 7 not found in topic "orders" on cluster "src"`), "partition not found"},
		{"refused", fmt.Errorf("fetch metadata for topic %q on cluster %q: %w", "orders", "src", refused), "connection refused"},
		{"timeout after a refused dial", fmt.Errorf("%w, last err: %w", kgo.ErrRecordTimeout, refused), "connection refused"},
		{"deadline", fmt.Errorf("list offsets: %w", context.DeadlineExceeded), "connection timed out"},
		{"unclassified", errors.New("broker 10.0.0.7:9092 closed the connection"), "broker not reachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := copyErrorText(tc.err)
			assert.Equal(t, tc.want, got)
			for _, raw := range []string{"10.0.0.7", "9092", "169.254", "dial", "connect:"} {
				assert.NotContains(t, got, raw)
			}
		})
	}
}
