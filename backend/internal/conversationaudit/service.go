package conversationaudit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const disableCaptureGrace = 30 * time.Second

const responseSegmentBytes = 32 << 10

type RuntimeState struct {
	Enabled               bool       `json:"enabled"`
	Lifecycle             string     `json:"lifecycle"`
	ConfigVersion         int64      `json:"config_version"`
	ActiveCaptures        int        `json:"active_captures"`
	BufferedBytes         int64      `json:"buffered_bytes"`
	MemoryBudgetBytes     int64      `json:"memory_budget_bytes"`
	PayloadQueueDepth     int        `json:"payload_queue_depth"`
	PayloadQueueCapacity  int        `json:"payload_queue_capacity"`
	MetadataQueueDepth    int        `json:"metadata_queue_depth"`
	MetadataQueueCapacity int        `json:"metadata_queue_capacity"`
	WorkersActive         int64      `json:"workers_active"`
	QueueFull             uint64     `json:"queue_full"`
	BudgetFull            uint64     `json:"budget_full"`
	EncodeFailed          uint64     `json:"encode_failed"`
	WriteFailed           uint64     `json:"write_failed"`
	LastError             string     `json:"last_error"`
	LastErrorAt           *time.Time `json:"last_error_at,omitempty"`
}

type CaptureService struct {
	manager    *ConfigManager
	repository *Repository
	owner      string

	activeConfig atomic.Pointer[ActiveConfig]
	lifecycle    atomic.Value
	shuttingDown atomic.Bool

	mu           sync.Mutex
	sessions     map[CaptureRef]*captureSession
	pool         *WorkerPool
	codec        *PayloadCodec
	budget       *MemoryBudget
	leases       *LeaseManager
	runCtx       context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	started      bool
	disableGrace time.Duration
}

func NewCaptureService(manager *ConfigManager, repository *Repository) *CaptureService {
	service := &CaptureService{
		manager: manager, repository: repository, owner: uuid.NewString(),
		sessions: make(map[CaptureRef]*captureSession), disableGrace: disableCaptureGrace,
	}
	service.lifecycle.Store("disabled")
	if manager != nil {
		manager.SetRuntime(service)
	}
	return service
}

func (s *CaptureService) Start(ctx context.Context) error {
	if s == nil || s.manager == nil || s.repository == nil {
		return errors.New("conversation audit service dependencies are unavailable")
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.runCtx, s.cancel = context.WithCancel(ctx)
	s.started = true
	s.mu.Unlock()
	partitionErr := s.repository.EnsurePartitions(s.runCtx, time.Now())
	configErr := s.manager.Start(s.runCtx)
	s.wg.Add(1)
	go s.maintenanceLoop(s.runCtx)
	if configErr != nil {
		return configErr
	}
	return partitionErr
}

func (s *CaptureService) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.shuttingDown.Store(true)
	s.activeConfig.Store(nil)
	if s.manager != nil {
		s.manager.Shutdown()
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	sessions := make([]*captureSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.mu.Unlock()
	for _, session := range sessions {
		session.detach("service_shutdown")
	}
	pool, leases, codec := s.takeRuntime()
	if pool != nil {
		pool.StopAccepting()
		_ = pool.Shutdown(ctx)
	}
	if leases != nil {
		leases.Shutdown()
	}
	if codec != nil {
		codec.Close()
	}
	s.wg.Wait()
	s.lifecycle.Store("disabled")
	return nil
}

func (s *CaptureService) Lifecycle() string {
	if s == nil {
		return "disabled"
	}
	value, _ := s.lifecycle.Load().(string)
	if value == "" {
		return "disabled"
	}
	return value
}

func (s *CaptureService) EffectiveEnabled() bool {
	return s != nil && s.activeConfig.Load() != nil && s.Lifecycle() == "enabled"
}

func (s *CaptureService) ApplyConfig(config ActiveConfig) {
	if s == nil {
		return
	}
	if s.shuttingDown.Load() {
		s.activeConfig.Store(nil)
		return
	}
	s.mu.Lock()
	if config.Enabled {
		if s.Lifecycle() == "disabling" {
			s.mu.Unlock()
			return
		}
		if s.pool == nil {
			keyring, err := s.manager.deployment.ParseKeyring()
			if err != nil || !keyring.Configured() {
				s.activeConfig.Store(nil)
				s.lifecycle.Store("disabled")
				s.mu.Unlock()
				s.manager.recordError("encryption_unavailable")
				return
			}
			codec, err := NewPayloadCodec(keyring, MaxPayloadMaxBytes, config.WorkerCount)
			if err != nil {
				s.mu.Unlock()
				s.manager.recordError("codec_unavailable")
				return
			}
			budget := NewMemoryBudget(config.MemoryBudgetBytes)
			pool, err := NewWorkerPool(s.repository, codec, budget, config.WorkerCount, config.QueueCapacity)
			if err != nil {
				codec.Close()
				s.mu.Unlock()
				s.manager.recordError("worker_pool_unavailable")
				return
			}
			leases := NewLeaseManager(s.repository, s.owner)
			if s.runCtx != nil {
				leases.Start(s.runCtx)
			}
			s.codec, s.budget, s.pool, s.leases = codec, budget, pool, leases
		} else {
			s.budget.SetLimit(config.MemoryBudgetBytes)
		}
		value := config
		s.activeConfig.Store(&value)
		s.lifecycle.Store("enabled")
		s.mu.Unlock()
		return
	}

	s.activeConfig.Store(nil)
	if s.pool == nil {
		s.lifecycle.Store("disabled")
		s.mu.Unlock()
		return
	}
	if s.Lifecycle() == "disabling" {
		s.mu.Unlock()
		return
	}
	s.lifecycle.Store("disabling")
	s.mu.Unlock()
	s.wg.Add(1)
	go s.finishDisable()
}

func (s *CaptureService) Begin(_ context.Context, input BeginInput) Session {
	config := s.activeConfig.Load()
	if config == nil || s.Lifecycle() != "enabled" {
		return sharedNoopSession
	}
	if input.AuditID == uuid.Nil {
		input.AuditID = uuid.New()
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	input.CreatedAt = NormalizeCreatedAt(input.CreatedAt)
	if input.RequestID == "" || input.UserID <= 0 || input.APIKeyID <= 0 || input.Protocol == "" || input.InboundEndpoint == "" {
		return sharedNoopSession
	}
	if input.TransportMode == "" {
		input.TransportMode = TransportHTTP
	}
	leaseExpires := time.Now().UTC().Add(defaultLeaseDuration)
	session := &captureSession{
		service: s, config: *config,
		ref: CaptureRef{CreatedAt: input.CreatedAt, AuditID: input.AuditID},
		record: RecordWrite{
			AuditID: input.AuditID, CreatedAt: input.CreatedAt, OwnerInstanceID: s.owner,
			LeaseExpiresAt: &leaseExpires, RequestID: input.RequestID, SessionID: input.SessionID,
			UserID: input.UserID, UserName: input.UserName, APIKeyID: input.APIKeyID, APIKeyName: input.APIKeyName,
			Protocol: input.Protocol, InboundEndpoint: input.InboundEndpoint, RequestedModel: input.RequestedModel,
			TransportMode: input.TransportMode, RecordState: RecordStateCapturing, CaptureStatus: CaptureMetadataOnly,
		},
		response: CanonicalConversation{Version: CanonicalVersion, Messages: []Message{}},
	}
	s.mu.Lock()
	if s.activeConfig.Load() != config || s.Lifecycle() != "enabled" || s.pool == nil {
		s.mu.Unlock()
		return sharedNoopSession
	}
	s.sessions[session.ref] = session
	pool, leases := s.pool, s.leases
	s.mu.Unlock()
	if leases != nil {
		leases.Register(session.ref)
	}
	if err := pool.Submit(&WriteJob{Record: session.recordSnapshot()}); err != nil {
		session.markDegraded("initial_write_queue_full")
	}
	return session
}

func (s *CaptureService) Runtime() RuntimeState {
	state := RuntimeState{Enabled: s.EffectiveEnabled(), Lifecycle: s.Lifecycle()}
	if config := s.activeConfig.Load(); config != nil {
		state.ConfigVersion = config.ConfigVersion
		state.MemoryBudgetBytes = config.MemoryBudgetBytes
	} else if s.manager != nil {
		if config, ok := s.manager.Active(); ok {
			state.ConfigVersion = config.ConfigVersion
			state.MemoryBudgetBytes = config.MemoryBudgetBytes
		}
	}
	s.mu.Lock()
	state.ActiveCaptures = len(s.sessions)
	pool, budget := s.pool, s.budget
	s.mu.Unlock()
	if budget != nil {
		state.BufferedBytes = budget.Used()
	}
	if pool != nil {
		state.PayloadQueueDepth, state.PayloadQueueCapacity, state.MetadataQueueDepth, state.MetadataQueueCapacity = pool.Runtime()
		metrics := pool.Metrics()
		state.WorkersActive = metrics.Active.Load()
		state.QueueFull = metrics.QueueFull.Load()
		state.BudgetFull = metrics.BudgetFull.Load()
		state.EncodeFailed = metrics.EncodeFailed.Load()
		state.WriteFailed = metrics.WriteFailed.Load()
	}
	if s.manager != nil {
		state.LastError, state.LastErrorAt = s.manager.RuntimeError()
	}
	return state
}

func (s *CaptureService) finishDisable() {
	defer s.wg.Done()
	deadline := time.NewTimer(s.disableGrace)
	poll := time.NewTicker(25 * time.Millisecond)
	defer deadline.Stop()
	defer poll.Stop()
	for {
		s.mu.Lock()
		remaining := len(s.sessions)
		s.mu.Unlock()
		if remaining == 0 {
			break
		}
		select {
		case <-deadline.C:
			s.detachAll("disabled_during_capture")
			remaining = 0
		case <-poll.C:
		}
		if remaining == 0 {
			break
		}
	}
	pool, leases, codec := s.takeRuntime()
	if pool != nil {
		pool.StopAccepting()
		drainCtx, cancel := context.WithTimeout(context.Background(), DefaultJobDeadline)
		_ = pool.Shutdown(drainCtx)
		cancel()
	}
	if leases != nil {
		leases.Shutdown()
	}
	if codec != nil {
		codec.Close()
	}
	s.lifecycle.Store("disabled")
	if s.shuttingDown.Load() || s.manager == nil {
		return
	}
	if config, ok := s.manager.Active(); ok && config.Enabled {
		s.ApplyConfig(config)
	}
}

func (s *CaptureService) takeRuntime() (*WorkerPool, *LeaseManager, *PayloadCodec) {
	s.mu.Lock()
	pool, leases, codec := s.pool, s.leases, s.codec
	s.pool, s.leases, s.codec, s.budget = nil, nil, nil, nil
	s.mu.Unlock()
	return pool, leases, codec
}

func (s *CaptureService) detachAll(reason string) {
	s.mu.Lock()
	sessions := make([]*captureSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.mu.Unlock()
	for _, session := range sessions {
		session.detach(reason)
	}
}

func (s *CaptureService) removeSession(session *captureSession) {
	s.mu.Lock()
	delete(s.sessions, session.ref)
	leases := s.leases
	s.mu.Unlock()
	if leases != nil {
		leases.Unregister(session.ref)
	}
}

func (s *CaptureService) currentPool() *WorkerPool {
	s.mu.Lock()
	pool := s.pool
	s.mu.Unlock()
	return pool
}

func (s *CaptureService) releaseReserved(bytes int64) {
	if bytes <= 0 {
		return
	}
	s.mu.Lock()
	budget := s.budget
	s.mu.Unlock()
	if budget != nil {
		budget.Release(bytes)
	}
}

func (s *CaptureService) maintenanceLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.repository.EnsurePartitions(ctx, time.Now())
			_, _ = s.repository.FinalizeExpiredCaptures(ctx, 1000)
			config, ok := s.manager.Active()
			if ok {
				_, _ = s.repository.DropExpiredPartitions(ctx, time.Now(), config.RetentionDays)
			}
		}
	}
}

type captureSession struct {
	service *CaptureService
	config  ActiveConfig
	ref     CaptureRef

	mu                sync.Mutex
	record            RecordWrite
	response          CanonicalConversation
	responseSegments  [][]byte
	responseBytes     int
	responseProtocol  string
	responseReserved  int64
	responseTruncated bool
	responseStopped   bool
	hasRequest        bool
	degradedReason    string
	finished          bool
}

func (s *captureSession) Annotate(patch MetadataPatch) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.finished {
		if patch.GroupID != nil {
			value := *patch.GroupID
			s.record.GroupID = &value
		}
		if patch.GroupName != "" {
			s.record.GroupName = patch.GroupName
		}
		if patch.AccountID != nil {
			value := *patch.AccountID
			s.record.AccountID = &value
		}
		if patch.AccountName != "" {
			s.record.AccountName = patch.AccountName
		}
		if patch.RequestedModel != "" {
			s.record.RequestedModel = patch.RequestedModel
		}
		if patch.EffectiveModel != "" {
			s.record.EffectiveModel = patch.EffectiveModel
		}
		if patch.TransportMode != "" {
			s.record.TransportMode = patch.TransportMode
		}
	}
	s.mu.Unlock()
}

func (s *captureSession) SetRequestBody(protocol string, body []byte) {
	if s == nil {
		return
	}
	result, err := ExtractRequest(protocol, body, s.config.RequestMaxBytes)
	if err != nil {
		reason := result.Reason
		if reason == "" {
			reason = "request_extract_failed"
		}
		s.markDegraded(reason)
		return
	}
	s.setRequest(result.Payload, result.Stats)
}

func (s *captureSession) SetRequest(payload CanonicalConversation) {
	limited, stats, err := LimitCanonical(payload, PayloadSideRequest, s.config.RequestMaxBytes)
	if err != nil {
		s.markDegraded("request_limit_failed")
		return
	}
	s.setRequest(limited, stats)
}

func (s *captureSession) setRequest(payload CanonicalConversation, stats CanonicalStats) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.finished || s.hasRequest {
		s.mu.Unlock()
		return
	}
	s.hasRequest = true
	record := cloneRecordWrite(s.record)
	s.mu.Unlock()
	pool := s.service.currentPool()
	if pool == nil || !pool.budget.TryReserve(int64(stats.StoredBytes)) {
		s.markDegraded("memory_budget_full")
		return
	}
	job := &WriteJob{
		Record: record, Side: PayloadSideRequest, Canonical: &payload,
		CanonicalStats: stats, reservedBytes: int64(stats.StoredBytes),
	}
	if err := pool.Submit(job); err != nil {
		s.markDegraded("request_queue_full")
	}
}

func (s *captureSession) Observe(event ResponseEvent) {
	if s == nil {
		return
	}
	reserve := responseEventBytes(event)
	s.mu.Lock()
	if s.finished || s.responseStopped {
		s.mu.Unlock()
		return
	}
	pool := s.service.currentPool()
	if pool == nil || !pool.budget.TryReserve(reserve) {
		s.degradedReason = "memory_budget_full"
		s.mu.Unlock()
		return
	}
	s.responseReserved += reserve
	if event.Message.Role != "" && len(event.Message.Content) > 0 {
		s.response.Messages = append(s.response.Messages, event.Message)
	}
	if event.Error != nil {
		value := *event.Error
		s.response.Error = &value
	}
	s.mu.Unlock()
}

func (s *captureSession) ObserveResponseBytes(protocol string, body []byte) {
	if s == nil || len(body) == 0 {
		return
	}
	s.mu.Lock()
	if s.finished || s.responseStopped {
		s.mu.Unlock()
		return
	}
	limit := responseRawLimit(s.config.ResponseMaxBytes)
	remaining := limit - s.responseBytes
	if remaining <= 0 {
		s.responseTruncated = true
		s.responseStopped = true
		s.mu.Unlock()
		return
	}
	if len(body) > remaining {
		body = body[:remaining]
		s.responseTruncated = true
		s.responseStopped = true
	}
	pool := s.service.currentPool()
	if pool == nil {
		s.responseStopped = true
		if s.degradedReason == "" {
			s.degradedReason = "worker_pool_unavailable"
		}
		s.mu.Unlock()
		return
	}
	for len(body) > 0 {
		var segment []byte
		if len(s.responseSegments) > 0 {
			segment = s.responseSegments[len(s.responseSegments)-1]
		}
		if len(segment) == cap(segment) {
			capacity := responseSegmentBytes
			if available := limit - s.responseBytes; capacity > available {
				capacity = available
			}
			if capacity <= 0 || !pool.budget.TryReserve(int64(capacity)) {
				s.responseStopped = true
				if s.degradedReason == "" {
					s.degradedReason = "memory_budget_full"
				}
				break
			}
			segment = make([]byte, 0, capacity)
			s.responseSegments = append(s.responseSegments, segment)
			s.responseReserved += int64(capacity)
		}
		space := cap(segment) - len(segment)
		count := len(body)
		if count > space {
			count = space
		}
		segment = append(segment, body[:count]...)
		s.responseSegments[len(s.responseSegments)-1] = segment
		s.responseBytes += count
		body = body[count:]
	}
	if s.responseProtocol == "" {
		s.responseProtocol = protocol
	}
	s.mu.Unlock()
}

func (s *captureSession) Finish(result FinishResult) {
	s.finish(result, "")
}

func (s *captureSession) detach(reason string) {
	s.finish(FinishResult{OutcomeStatus: OutcomeUnknown, CaptureStatus: CaptureDegraded, DegradedReason: reason}, reason)
}

func (s *captureSession) finish(result FinishResult, forcedReason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now().UTC()
	}
	if result.OutcomeStatus == "" {
		result.OutcomeStatus = OutcomeUnknown
	}
	if forcedReason != "" {
		s.degradedReason = forcedReason
	}
	response := s.response
	responseSegments := s.responseSegments
	responseProtocol := s.responseProtocol
	responseTruncated := s.responseTruncated
	reserved := s.responseReserved
	s.responseReserved = 0
	record := cloneRecordWrite(s.record)
	hasResponse := len(responseSegments) > 0 || len(response.Messages) > 0 || response.Error != nil
	hasRequest := s.hasRequest
	degraded := s.degradedReason
	s.mu.Unlock()

	s.service.removeSession(s)
	completedAt := result.CompletedAt.UTC()
	mutableUntil := completedAt.Add(DefaultJobDeadline)
	record.RecordState = RecordStateFinalized
	record.CompletedAt = &completedAt
	record.MutableUntil = &mutableUntil
	record.LeaseExpiresAt = nil
	outcome := result.OutcomeStatus
	record.OutcomeStatus = &outcome
	if result.HTTPStatus > 0 {
		status := result.HTTPStatus
		record.HTTPStatus = &status
	}
	record.ErrorCode = result.ErrorCode
	record.CaptureStatus = result.CaptureStatus
	if record.CaptureStatus == "" {
		if hasResponse || hasRequest {
			record.CaptureStatus = CaptureComplete
		} else {
			record.CaptureStatus = CaptureMetadataOnly
		}
	}
	if degraded != "" || result.DegradedReason != "" {
		record.CaptureStatus = CaptureDegraded
		record.DegradedReason = degraded
		if result.DegradedReason != "" {
			record.DegradedReason = result.DegradedReason
		}
	}
	job := &WriteJob{Record: record, reservedBytes: reserved}
	if len(responseSegments) > 0 {
		job.Side = PayloadSideResponse
		job.RawSegments = responseSegments
		job.Protocol = responseProtocol
		job.MaxBytes = s.config.ResponseMaxBytes
		job.RawTruncated = responseTruncated
		if responseTruncated && job.Record.CaptureStatus != CaptureDegraded {
			job.Record.CaptureStatus = CaptureTruncated
		}
	} else if hasResponse {
		limited, stats, err := LimitCanonical(response, PayloadSideResponse, s.config.ResponseMaxBytes)
		if err == nil {
			job.Side, job.Canonical, job.CanonicalStats = PayloadSideResponse, &limited, stats
			if stats.Truncated && job.Record.CaptureStatus != CaptureDegraded {
				job.Record.CaptureStatus = CaptureTruncated
			}
		} else {
			job.Record.CaptureStatus = CaptureDegraded
			job.Record.DegradedReason = "response_limit_failed"
		}
	}
	pool := s.service.currentPool()
	if pool == nil {
		s.service.releaseReserved(reserved)
		return
	}
	_ = pool.Submit(job)
}

func responseRawLimit(maxBytes int) int {
	if maxBytes <= 0 {
		return 0
	}
	return maxBytes + maxBytes/2
}

func responseEventBytes(event ResponseEvent) int64 {
	var size int64
	for _, item := range event.Message.Content {
		size += int64(len(item.Type) + len(item.Text) + len(item.Name) + len(item.Arguments) + len(item.Content) + len(item.URL) + 64)
	}
	if event.Error != nil {
		size += int64(len(event.Error.Code) + len(event.Error.Message) + 64)
	}
	return size
}

func (s *captureSession) markDegraded(reason string) {
	if s == nil || reason == "" {
		return
	}
	s.mu.Lock()
	if !s.finished && s.degradedReason == "" {
		s.degradedReason = reason
	}
	s.mu.Unlock()
}

func (s *captureSession) recordSnapshot() RecordWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneRecordWrite(s.record)
}

func cloneRecordWrite(record RecordWrite) RecordWrite {
	clone := record
	if record.GroupID != nil {
		value := *record.GroupID
		clone.GroupID = &value
	}
	if record.AccountID != nil {
		value := *record.AccountID
		clone.AccountID = &value
	}
	return clone
}
