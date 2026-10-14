package requestaudit

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const eventOverhead int64 = 512
const maxEventAge = time.Minute

type Option func(*Manager)

func WithBufferBytes(v int64) Option {
	return func(m *Manager) {
		if v > 0 {
			m.bufferBytes = v
		}
	}
}
func WithMaxEvents(v int) Option {
	return func(m *Manager) {
		if v > 0 {
			m.maxEvents = v
		}
	}
}
func WithWriteTimeout(v time.Duration) Option {
	return func(m *Manager) {
		if v > 0 {
			m.writeTimeout = v
		}
	}
}
func WithPolicyDB(db *sql.DB) Option {
	return func(m *Manager) {
		if db != nil {
			m.policyDB = db
		}
	}
}
func WithBanLookupTimeout(v time.Duration) Option {
	return func(m *Manager) {
		if v > 0 {
			m.banLookupTimeout = v
		}
	}
}
func WithInstanceID(v string) Option {
	return func(m *Manager) {
		if strings.TrimSpace(v) != "" {
			m.instanceID = strings.TrimSpace(v)
		}
	}
}

// Manager leaves connection pool sizing and closing to the application.
type Manager struct {
	db, policyDB                                                      *sql.DB
	bufferBytes                                                       int64
	maxEvents                                                         int
	writeTimeout, banLookupTimeout                                    time.Duration
	instanceID                                                        string
	mu                                                                sync.RWMutex
	config                                                            Config
	configKnown, running                                              bool
	ctx                                                               context.Context
	cancel                                                            context.CancelFunc
	writeCtx                                                          context.Context
	writeCancel                                                       context.CancelFunc
	done                                                              chan struct{}
	queue                                                             chan auditEvent
	wg                                                                sync.WaitGroup
	queueBytes, queueEvents, accepted, droppedRequests, droppedEvents atomic.Int64
	writeFailures, writtenEvents, banCheckFailures, lastWrite         atomic.Int64
	lastCleanup, cleanupDeleted, cleanupFailures                      atomic.Int64
	circuitUntil, lastWarning                                         atomic.Int64
}

type auditEvent struct {
	kind, id, part                     string
	meta                               *RequestMeta
	identity                           *Identity
	completion                         *Completion
	seq                                int
	raw                                []byte
	manifest                           *PartManifest
	manifests                          map[string]PartManifest
	observedBodyBytes, storedBodyBytes int64
	captureState                       string
	queued                             int64
	createdAt                          time.Time
}

func NewManager(db *sql.DB, options ...Option) *Manager {
	host, _ := os.Hostname()
	m := &Manager{db: db, bufferBytes: 64 * 1024 * 1024, maxEvents: 8192,
		writeTimeout: time.Second, banLookupTimeout: 20 * time.Millisecond,
		instanceID: host, config: DefaultConfig()}
	for _, option := range options {
		if option != nil {
			option(m)
		}
	}
	if m.policyDB == nil {
		m.policyDB = db
	}
	if m.instanceID == "" {
		m.instanceID = "requestaudit"
	}
	return m
}

func (m *Manager) Start(ctx context.Context) error {
	if m == nil || m.db == nil {
		return failure(503, "AUDIT_STORAGE_UNAVAILABLE", "Request audit storage unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	loadCtx, cancel := context.WithTimeout(ctx, m.writeTimeout)
	cfg, configErr := m.loadConfig(loadCtx)
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return nil
	}
	if m.done != nil {
		select {
		case <-m.done:
		default:
			return failure(503, "AUDIT_STORAGE_UNAVAILABLE", "Request audit is stopping")
		}
	}
	if configErr == nil {
		m.config, m.configKnown = cfg, true
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.writeCtx, m.writeCancel = context.WithCancel(context.Background())
	writeCancel := m.writeCancel
	context.AfterFunc(m.ctx, func() { time.AfterFunc(5*time.Second, writeCancel) })
	m.queue = make(chan auditEvent, m.maxEvents)
	m.done = make(chan struct{})
	m.running = true
	m.wg.Add(2)
	go m.writerLoop(m.ctx, m.writeCtx, m.queue)
	go m.maintenanceLoop(m.ctx)
	go func(done chan struct{}) { m.wg.Wait(); close(done) }(m.done)
	// Default-off startup survives a missing database. State exposes unknown config.
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.cancel != nil {
		m.running = false
		m.cancel()
		m.cancel = nil
	}
	done := m.done
	m.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) Enabled() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running && m.configKnown && m.config.Enabled
}

func (m *Manager) SessionBanEnabled() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.configKnown && m.config.SessionBanEnabled
}

func (m *Manager) Status() Status {
	if m == nil {
		return Status{}
	}
	m.mu.RLock()
	cfg, known, running := m.config, m.configKnown, m.running
	m.mu.RUnlock()
	return Status{InstanceID: m.instanceID, Running: running, ConfigKnown: known, Config: cfg,
		QueueBytes: m.queueBytes.Load(), BufferBytes: m.bufferBytes, QueueEvents: int(m.queueEvents.Load()),
		AcceptedRequests: m.accepted.Load(), DroppedRequests: m.droppedRequests.Load(),
		DroppedEvents: m.droppedEvents.Load(), WriteFailures: m.writeFailures.Load(),
		WrittenEvents: m.writtenEvents.Load(), BanCheckFailures: m.banCheckFailures.Load(),
		LastWriteAt: unixTime(m.lastWrite.Load()), LastCleanupAt: unixTime(m.lastCleanup.Load()),
		CleanupDeleted: m.cleanupDeleted.Load(), CleanupFailures: m.cleanupFailures.Load(),
		WriterBackoffUntil: unixTime(m.circuitUntil.Load())}
}

func unixTime(v int64) *time.Time {
	if v == 0 {
		return nil
	}
	t := time.Unix(0, v).UTC()
	return &t
}

// A reservation remains charged until persistence/drop, including in-flight work.
func (m *Manager) reserve(n int64, payload bool) bool {
	if payload && time.Now().UnixNano() < m.circuitUntil.Load() {
		return false
	}
	limit := m.bufferBytes
	if payload {
		limit -= limit / 8
	} // terminal events retain reserved headroom
	if n < 0 || n > limit {
		return false
	}
	for {
		used := m.queueBytes.Load()
		if used > limit-n {
			return false
		}
		if m.queueBytes.CompareAndSwap(used, used+n) {
			return true
		}
	}
}

func (m *Manager) enqueue(e auditEvent) bool {
	e.queued = eventOverhead + int64(len(e.raw))
	if e.meta != nil {
		e.queued += int64(len(e.meta.Method) + len(e.meta.RoutePattern) + len(e.meta.Protocol) + len(e.meta.ClientRequestID) + len(e.meta.ConnectionID) + len(e.meta.MessageType) + len(e.meta.Host) + len(e.meta.RequestURI))
	}
	if e.identity != nil {
		e.queued += int64(len(e.identity.SessionID) + len(e.identity.SessionSource) + len(e.identity.SessionResolution) + len(e.identity.Model))
	}
	if !m.reserve(e.queued, len(e.raw) > 0) {
		m.droppedEvents.Add(1)
		m.warn("capture buffer exhausted; requests continue without complete audit capture")
		return false
	}
	return m.enqueueReserved(e)
}

func (m *Manager) enqueueReserved(e auditEvent) bool {
	if e.createdAt.IsZero() {
		e.createdAt = time.Now().UTC()
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.running {
		limit := int64(m.maxEvents)
		if len(e.raw) > 0 {
			limit -= limit / 8
		}
		for {
			used := m.queueEvents.Load()
			if used >= limit {
				m.queueBytes.Add(-e.queued)
				m.droppedEvents.Add(1)
				return false
			}
			if m.queueEvents.CompareAndSwap(used, used+1) {
				break
			}
		}
		select {
		case m.queue <- e:
			return true
		default:
			m.queueEvents.Add(-1)
		}
	}
	m.queueBytes.Add(-e.queued)
	m.droppedEvents.Add(1)
	return false
}

func (m *Manager) releaseEvents(events []auditEvent) {
	for i := range events {
		m.queueBytes.Add(-events[i].queued)
		m.queueEvents.Add(-1)
		events[i] = auditEvent{}
	}
}

func (m *Manager) writerLoop(ctx, writeCtx context.Context, queue <-chan auditEvent) {
	defer m.wg.Done()
	for {
		var first auditEvent
		select {
		case <-ctx.Done():
			m.drain(writeCtx, queue)
			return
		case first = <-queue:
		}
		batch := []auditEvent{first}
		size := len(first.raw)
		for len(batch) < 100 && size < 2*1024*1024 {
			select {
			case next := <-queue:
				size += len(next.raw)
				batch = append(batch, next)
			default:
				goto flush
			}
		}
	flush:
		m.flush(writeCtx, batch)
	}
}

func (m *Manager) flush(writeCtx context.Context, batch []auditEvent) {
	defer m.releaseEvents(batch)
	live := make([]auditEvent, 0, len(batch))
	for _, event := range batch {
		if time.Since(event.createdAt) <= maxEventAge {
			live = append(live, event)
		} else {
			m.droppedEvents.Add(1)
		}
	}
	if len(live) == 0 {
		return
	}
	if time.Now().UnixNano() < m.circuitUntil.Load() || writeCtx.Err() != nil {
		m.droppedEvents.Add(int64(len(live)))
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(writeCtx, m.writeTimeout)
		var err error
		for len(live) > 0 {
			err = m.persistBatch(ctx, live)
			var rejected *rejectedEventError
			if !errors.As(err, &rejected) {
				break
			}
			// Rollback has already undone the failed batch. Discard only this
			// request's events and retry healthy requests within the same deadline.
			kept := live[:0]
			for _, e := range live {
				if e.id != rejected.id {
					kept = append(kept, e)
				}
			}
			dropped := int64(len(live) - len(kept))
			m.droppedEvents.Add(dropped)
			m.writeFailures.Add(dropped)
			m.droppedRequests.Add(1)
			m.warn("invalid audit events discarded; unrelated request capture continues")
			live = kept
		}
		cancel()
		if len(live) == 0 {
			return
		}
		if err == nil {
			m.writtenEvents.Add(int64(len(live)))
			m.lastWrite.Store(time.Now().UnixNano())
			m.circuitUntil.Store(0)
			return
		}
		if !retryable(err) {
			break
		}
		if attempt < 2 {
			select {
			case <-writeCtx.Done():
			case <-time.After(time.Duration(attempt+1) * 25 * time.Millisecond):
			}
		}
	}
	m.writeFailures.Add(int64(len(live)))
	m.circuitUntil.Store(time.Now().Add(time.Second).UnixNano())
	m.warn("persistence failed; capture is temporarily degraded")
}

func (m *Manager) drain(ctx context.Context, queue <-chan auditEvent) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case e := <-queue:
			if time.Now().Before(deadline) {
				m.flush(ctx, []auditEvent{e})
			} else {
				m.releaseEvents([]auditEvent{e})
				m.droppedEvents.Add(1)
			}
		default:
			return
		}
	}
}

func (m *Manager) maintenanceLoop(ctx context.Context) {
	defer m.wg.Done()
	refresh, cleanup := time.NewTicker(30*time.Second), time.NewTicker(5*time.Minute)
	defer refresh.Stop()
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			loadCtx, cancel := context.WithTimeout(ctx, m.writeTimeout)
			cfg, err := m.loadConfig(loadCtx)
			cancel()
			if err == nil {
				m.mu.Lock()
				if !m.configKnown || cfg.Version >= m.config.Version {
					m.config, m.configKnown = cfg, true
				}
				m.mu.Unlock()
			} else {
				m.warn("configuration refresh failed; retaining the last known setting")
			}
		case <-cleanup.C:
			m.cleanupOnce(ctx)
		}
	}
}

// Only fixed messages are logged; never include payloads, session IDs or DB errors.
func (m *Manager) warn(message string) {
	now := time.Now().UnixNano()
	last := m.lastWarning.Load()
	if now-last > int64(time.Minute) && m.lastWarning.CompareAndSwap(last, now) {
		log.Printf("[requestaudit] %s", message)
	}
}
