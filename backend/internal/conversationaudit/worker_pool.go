package conversationaudit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrWorkerQueueFull   = errors.New("conversation audit worker queue full")
	ErrMemoryBudgetFull  = errors.New("conversation audit memory budget full")
	ErrWorkerPoolStopped = errors.New("conversation audit worker pool stopped")
)

type RecordWriter interface {
	Upsert(context.Context, RecordWrite) error
}

type WorkerMetrics struct {
	Submitted    atomic.Uint64
	Completed    atomic.Uint64
	Expired      atomic.Uint64
	QueueFull    atomic.Uint64
	BudgetFull   atomic.Uint64
	EncodeFailed atomic.Uint64
	WriteFailed  atomic.Uint64
	Panics       atomic.Uint64
	Active       atomic.Int64
}

type WorkerPool struct {
	repository RecordWriter
	codec      *PayloadCodec
	budget     *MemoryBudget
	queue      chan *WriteJob
	metadata   chan *WriteJob
	metrics    *WorkerMetrics
	now        func() time.Time

	accepting atomic.Bool
	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func NewWorkerPool(repository RecordWriter, codec *PayloadCodec, budget *MemoryBudget, workers, queueCapacity int) (*WorkerPool, error) {
	if repository == nil || codec == nil || budget == nil {
		return nil, errors.New("conversation audit worker dependencies are unavailable")
	}
	if workers < 1 || workers > 8 || queueCapacity < 1 {
		return nil, errors.New("conversation audit worker shape is invalid")
	}
	metadataCapacity := queueCapacity / 16
	if metadataCapacity < 64 {
		metadataCapacity = 64
	}
	if metadataCapacity > 256 {
		metadataCapacity = 256
	}
	pool := &WorkerPool{
		repository: repository, codec: codec, budget: budget,
		queue: make(chan *WriteJob, queueCapacity), metadata: make(chan *WriteJob, metadataCapacity),
		metrics: &WorkerMetrics{}, now: time.Now,
	}
	pool.accepting.Store(true)
	pool.start(workers)
	return pool, nil
}

func (p *WorkerPool) start(workers int) {
	p.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		p.cancel = cancel
		for workerID := 0; workerID < workers; workerID++ {
			p.wg.Add(1)
			go p.runWorker(ctx)
		}
	})
}

func (p *WorkerPool) Submit(job *WriteJob) error {
	if p == nil || job == nil || !p.accepting.Load() {
		if p != nil && job != nil && job.reservedBytes > 0 {
			p.budget.Release(job.reservedBytes)
			job.reservedBytes = 0
		}
		return ErrWorkerPoolStopped
	}
	job.normalize(p.now())
	reserved := job.reservedBytes
	required := int64(0)
	if job.Canonical != nil {
		required = int64(job.CanonicalStats.StoredBytes)
	} else if job.RawPayload != nil {
		required = int64(len(job.RawPayload))
	} else if job.RawSegments != nil {
		for _, segment := range job.RawSegments {
			required += int64(cap(segment))
		}
	}
	if reserved < required {
		if !p.budget.TryReserve(required - reserved) {
			p.metrics.BudgetFull.Add(1)
			p.budget.Release(reserved)
			job.reservedBytes = 0
			return ErrMemoryBudgetFull
		}
		reserved = required
	}
	job.reservedBytes = reserved
	channel := p.queue
	if job.Canonical == nil && job.RawPayload == nil && job.RawSegments == nil {
		channel = p.metadata
	}
	select {
	case channel <- job:
		p.metrics.Submitted.Add(1)
		return nil
	default:
		p.budget.Release(job.reservedBytes)
		job.reservedBytes = 0
		p.metrics.QueueFull.Add(1)
		return ErrWorkerQueueFull
	}
}

func (p *WorkerPool) Runtime() (payloadDepth, payloadCapacity, metadataDepth, metadataCapacity int) {
	if p == nil {
		return 0, 0, 0, 0
	}
	return len(p.queue), cap(p.queue), len(p.metadata), cap(p.metadata)
}

func (p *WorkerPool) Metrics() *WorkerMetrics {
	if p == nil {
		return nil
	}
	return p.metrics
}

func (p *WorkerPool) StopAccepting() {
	if p != nil {
		p.accepting.Store(false)
	}
}

func (p *WorkerPool) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.StopAccepting()
	var result error
	p.stopOnce.Do(func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for len(p.queue) > 0 || len(p.metadata) > 0 || p.metrics.Active.Load() > 0 {
			select {
			case <-ctx.Done():
				result = ctx.Err()
				p.cancel()
				p.releaseQueued()
				p.wg.Wait()
				return
			case <-ticker.C:
			}
		}
		p.cancel()
		p.wg.Wait()
	})
	return result
}

func (p *WorkerPool) runWorker(ctx context.Context) {
	defer p.wg.Done()
	for {
		job, ok := p.nextJob(ctx)
		if !ok {
			return
		}
		p.metrics.Active.Add(1)
		p.processSafely(ctx, job)
		p.metrics.Active.Add(-1)
		p.budget.Release(job.reservedBytes)
		job.reservedBytes = 0
	}
}

func (p *WorkerPool) nextJob(ctx context.Context) (*WriteJob, bool) {
	select {
	case job := <-p.metadata:
		return job, true
	default:
	}
	select {
	case <-ctx.Done():
		return nil, false
	case job := <-p.metadata:
		return job, true
	case job := <-p.queue:
		return job, true
	}
}

func (p *WorkerPool) processSafely(ctx context.Context, job *WriteJob) {
	defer func() {
		if recover() != nil {
			p.metrics.Panics.Add(1)
		}
	}()
	now := p.now()
	if !job.Deadline.After(now) {
		p.metrics.Expired.Add(1)
		return
	}
	if job.Canonical == nil && (job.RawPayload != nil || job.RawSegments != nil) {
		var result ExtractResult
		var err error
		if job.RawSegments != nil {
			result, err = ExtractResponseSegments(job.Protocol, job.RawSegments, job.MaxBytes)
		} else {
			result, err = ExtractResponse(job.Protocol, job.RawPayload, job.MaxBytes)
		}
		if err != nil {
			job.Record.CaptureStatus = CaptureDegraded
			job.Record.DegradedReason = result.Reason
			if job.Record.DegradedReason == "" {
				job.Record.DegradedReason = "response_extract_failed"
			}
		} else {
			if job.RawTruncated {
				result.Payload.Truncated = true
				result.Payload, result.Stats, err = LimitCanonical(result.Payload, PayloadSideResponse, job.MaxBytes)
			}
			if err != nil {
				job.Record.CaptureStatus = CaptureDegraded
				job.Record.DegradedReason = "response_limit_failed"
			} else {
				job.Canonical, job.CanonicalStats = &result.Payload, result.Stats
				if result.Payload.Error != nil && job.Record.ErrorCode == "" {
					job.Record.ErrorCode = result.Payload.Error.Code
				}
				if (job.RawTruncated || result.Stats.Truncated) && job.Record.CaptureStatus != CaptureDegraded {
					job.Record.CaptureStatus = CaptureTruncated
				}
			}
		}
	}
	if job.Canonical != nil {
		encoded, err := p.codec.Encode(RecordIdentity{AuditID: job.Record.AuditID, CreatedAt: job.Record.CreatedAt}, job.Side, *job.Canonical)
		if err != nil {
			p.metrics.EncodeFailed.Add(1)
			return
		}
		job.Record.Payload = &StoredPayload{
			Side: job.Side, CodecVersion: encoded.CodecVersion, KeyID: encoded.KeyID, Data: encoded.Data,
			OriginalBytes: int64(job.CanonicalStats.OriginalBytes), StoredBytes: int64(job.CanonicalStats.StoredBytes),
			CompressedBytes: encoded.CompressedBytes, EncryptedBytes: encoded.EncryptedBytes,
			Truncated: job.CanonicalStats.Truncated, OmittedMessages: job.CanonicalStats.OmittedMessages,
			OmittedBytes: job.CanonicalStats.OmittedBytes,
		}
		if job.Record.Payload.OriginalBytes == 0 {
			job.Record.Payload.OriginalBytes = encoded.OriginalBytes
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		if !job.Deadline.After(p.now()) {
			p.metrics.Expired.Add(1)
			return
		}
		writeCtx, cancel := context.WithDeadline(ctx, job.Deadline)
		err := p.repository.Upsert(writeCtx, job.Record)
		cancel()
		if err == nil {
			p.metrics.Completed.Add(1)
			return
		}
		if attempt < 2 {
			delay := time.Duration(50*(1<<attempt)) * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				p.metrics.WriteFailed.Add(1)
				return
			case <-timer.C:
			}
		}
	}
	p.metrics.WriteFailed.Add(1)
}

func (p *WorkerPool) releaseQueued() {
	for {
		select {
		case job := <-p.metadata:
			p.budget.Release(job.reservedBytes)
		case job := <-p.queue:
			p.budget.Release(job.reservedBytes)
		default:
			return
		}
	}
}
