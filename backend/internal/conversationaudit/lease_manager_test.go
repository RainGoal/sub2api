package conversationaudit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type leaseRepositoryStub struct {
	mu      sync.Mutex
	batches [][]CaptureRef
}

func (r *leaseRepositoryStub) RenewLeases(_ context.Context, _ string, refs []CaptureRef, _ time.Time) error {
	r.mu.Lock()
	r.batches = append(r.batches, append([]CaptureRef(nil), refs...))
	r.mu.Unlock()
	return nil
}

func TestLeaseManagerBatchesOneCentralRegistry(t *testing.T) {
	repository := &leaseRepositoryStub{}
	manager := NewLeaseManager(repository, "instance-1")
	createdAt := time.Now().UTC()
	refs := make([]CaptureRef, 0, defaultLeaseBatchSize+1)
	for i := 0; i <= defaultLeaseBatchSize; i++ {
		ref := CaptureRef{CreatedAt: createdAt, AuditID: uuid.New()}
		refs = append(refs, ref)
		manager.Register(ref)
	}
	require.Equal(t, defaultLeaseBatchSize+1, manager.Count())
	manager.renewOnce(context.Background())
	require.Len(t, repository.batches, 2)
	require.Len(t, repository.batches[0], defaultLeaseBatchSize)
	require.Len(t, repository.batches[1], 1)
	manager.Unregister(refs[0])
	require.Equal(t, defaultLeaseBatchSize, manager.Count())
}

func TestLeaseManagerLifecycleUsesOneLoop(t *testing.T) {
	repository := &leaseRepositoryStub{}
	manager := NewLeaseManager(repository, "instance-1")
	manager.interval = 5 * time.Millisecond
	manager.Start(context.Background())
	manager.Register(CaptureRef{CreatedAt: time.Now(), AuditID: uuid.New()})
	require.Eventually(t, func() bool {
		repository.mu.Lock()
		defer repository.mu.Unlock()
		return len(repository.batches) > 0
	}, time.Second, 10*time.Millisecond)
	manager.Shutdown()
	manager.Shutdown()
}
