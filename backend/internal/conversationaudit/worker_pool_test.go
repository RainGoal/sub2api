package conversationaudit

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type workerTestRepository struct {
	block      chan struct{}
	started    chan struct{}
	startOnce  sync.Once
	mu         sync.Mutex
	writes     []RecordWrite
	panicFirst bool
}

func (r *workerTestRepository) Upsert(_ context.Context, write RecordWrite) error {
	r.startOnce.Do(func() { close(r.started) })
	if r.panicFirst {
		r.panicFirst = false
		panic("worker test panic")
	}
	if r.block != nil {
		<-r.block
	}
	r.mu.Lock()
	r.writes = append(r.writes, write)
	r.mu.Unlock()
	return nil
}

func newWorkerTestCodec(t *testing.T) *PayloadCodec {
	t.Helper()
	keys := &testKeyring{active: "v1", keys: map[string][]byte{"v1": []byte(strings.Repeat("1", 32))}}
	codec, err := NewPayloadCodec(keys, 1<<20, 1)
	require.NoError(t, err)
	t.Cleanup(codec.Close)
	return codec
}

func workerTestJob() *WriteJob {
	payload := CanonicalConversation{Version: CanonicalVersion, Messages: []Message{}}
	return &WriteJob{
		Record: RecordWrite{
			AuditID: uuid.New(), CreatedAt: time.Now(), OwnerInstanceID: "owner",
			RequestID: "request", UserID: 1, APIKeyID: 2, Protocol: "openai",
			InboundEndpoint: "/v1/responses", TransportMode: TransportHTTP,
			RecordState: RecordStateCapturing, CaptureStatus: CaptureMetadataOnly,
		},
		Side: PayloadSideRequest, Canonical: &payload,
		CanonicalStats: CanonicalStats{StoredBytes: 128},
	}
}

func TestWorkerPoolQueueSaturationIsNonBlocking(t *testing.T) {
	repository := &workerTestRepository{block: make(chan struct{}), started: make(chan struct{})}
	budget := NewMemoryBudget(1 << 20)
	pool, err := NewWorkerPool(repository, newWorkerTestCodec(t), budget, 1, 1)
	require.NoError(t, err)

	require.NoError(t, pool.Submit(workerTestJob()))
	select {
	case <-repository.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	require.NoError(t, pool.Submit(workerTestJob()))
	started := time.Now()
	require.ErrorIs(t, pool.Submit(workerTestJob()), ErrWorkerQueueFull)
	require.Less(t, time.Since(started), 50*time.Millisecond)

	close(repository.block)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, pool.Shutdown(ctx))
	require.Zero(t, budget.Used())
}

func TestWorkerPoolBudgetSaturationIsNonBlocking(t *testing.T) {
	repository := &workerTestRepository{started: make(chan struct{})}
	budget := NewMemoryBudget(64)
	pool, err := NewWorkerPool(repository, newWorkerTestCodec(t), budget, 1, 1)
	require.NoError(t, err)
	require.ErrorIs(t, pool.Submit(workerTestJob()), ErrMemoryBudgetFull)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, pool.Shutdown(ctx))
}

func TestWorkerPoolRecoversWorkerPanic(t *testing.T) {
	repository := &workerTestRepository{started: make(chan struct{}), panicFirst: true}
	budget := NewMemoryBudget(1 << 20)
	pool, err := NewWorkerPool(repository, newWorkerTestCodec(t), budget, 1, 4)
	require.NoError(t, err)
	require.NoError(t, pool.Submit(workerTestJob()))
	require.Eventually(t, func() bool { return pool.Metrics().Panics.Load() == 1 }, time.Second, 10*time.Millisecond)
	require.NoError(t, pool.Submit(workerTestJob()))
	require.Eventually(t, func() bool { return pool.Metrics().Completed.Load() == 1 }, time.Second, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, pool.Shutdown(ctx))
}
