package conversationaudit

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultLeaseRenewInterval = 30 * time.Second
	defaultLeaseDuration      = 2 * time.Minute
	defaultLeaseBatchSize     = 1000
)

type LeaseRepository interface {
	RenewLeases(context.Context, string, []CaptureRef, time.Time) error
}

type LeaseManager struct {
	repository LeaseRepository
	owner      string
	interval   time.Duration
	duration   time.Duration
	now        func() time.Time

	mu        sync.RWMutex
	active    map[CaptureRef]struct{}
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once
}

func NewLeaseManager(repository LeaseRepository, owner string) *LeaseManager {
	return &LeaseManager{
		repository: repository, owner: owner, interval: defaultLeaseRenewInterval,
		duration: defaultLeaseDuration, now: time.Now, active: make(map[CaptureRef]struct{}),
	}
}

func (m *LeaseManager) Start(ctx context.Context) {
	if m == nil || m.repository == nil || m.owner == "" {
		return
	}
	m.startOnce.Do(func() {
		runCtx, cancel := context.WithCancel(ctx)
		m.cancel = cancel
		m.wg.Add(1)
		go m.run(runCtx)
	})
}

func (m *LeaseManager) Register(ref CaptureRef) {
	if m == nil || ref.AuditID == uuid.Nil || ref.CreatedAt.IsZero() {
		return
	}
	ref.CreatedAt = NormalizeCreatedAt(ref.CreatedAt)
	m.mu.Lock()
	m.active[ref] = struct{}{}
	m.mu.Unlock()
}

func (m *LeaseManager) Unregister(ref CaptureRef) {
	if m == nil {
		return
	}
	ref.CreatedAt = NormalizeCreatedAt(ref.CreatedAt)
	m.mu.Lock()
	delete(m.active, ref)
	m.mu.Unlock()
}

func (m *LeaseManager) Count() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	count := len(m.active)
	m.mu.RUnlock()
	return count
}

func (m *LeaseManager) Shutdown() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() {
		if m.cancel != nil {
			m.cancel()
		}
		m.wg.Wait()
	})
}

func (m *LeaseManager) run(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.renewOnce(ctx)
		}
	}
}

func (m *LeaseManager) renewOnce(ctx context.Context) {
	refs := m.snapshot()
	expires := m.now().UTC().Add(m.duration)
	for start := 0; start < len(refs); start += defaultLeaseBatchSize {
		end := start + defaultLeaseBatchSize
		if end > len(refs) {
			end = len(refs)
		}
		_ = m.repository.RenewLeases(ctx, m.owner, refs[start:end], expires)
	}
}

func (m *LeaseManager) snapshot() []CaptureRef {
	m.mu.RLock()
	refs := make([]CaptureRef, 0, len(m.active))
	for ref := range m.active {
		refs = append(refs, ref)
	}
	m.mu.RUnlock()
	return refs
}
