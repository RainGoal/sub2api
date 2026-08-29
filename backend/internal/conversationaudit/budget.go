package conversationaudit

import (
	"sync/atomic"
)

const bufferSegmentBytes = 8 * 1024

type MemoryBudget struct {
	limit atomic.Int64
	used  atomic.Int64
}

func NewMemoryBudget(limit int64) *MemoryBudget {
	budget := &MemoryBudget{}
	budget.limit.Store(limit)
	return budget
}

func (b *MemoryBudget) SetLimit(limit int64) {
	if b != nil && limit >= 0 {
		b.limit.Store(limit)
	}
}

func (b *MemoryBudget) Limit() int64 {
	if b == nil {
		return 0
	}
	return b.limit.Load()
}

func (b *MemoryBudget) Used() int64 {
	if b == nil {
		return 0
	}
	return b.used.Load()
}

func (b *MemoryBudget) TryReserve(bytes int64) bool {
	if b == nil || bytes < 0 {
		return false
	}
	if bytes == 0 {
		return true
	}
	for {
		used := b.used.Load()
		limit := b.limit.Load()
		if bytes > limit || used > limit-bytes {
			return false
		}
		if b.used.CompareAndSwap(used, used+bytes) {
			return true
		}
	}
}

func (b *MemoryBudget) Release(bytes int64) {
	if b == nil || bytes <= 0 {
		return
	}
	for {
		used := b.used.Load()
		next := used - bytes
		if next < 0 {
			next = 0
		}
		if b.used.CompareAndSwap(used, next) {
			return
		}
	}
}

type SegmentedBuffer struct {
	budget   *MemoryBudget
	segments [][]byte
	size     int
	reserved int64
	closed   bool
}

func NewSegmentedBuffer(budget *MemoryBudget) *SegmentedBuffer {
	return &SegmentedBuffer{budget: budget}
}

func (b *SegmentedBuffer) Append(value []byte) (int, bool) {
	if b == nil || b.closed || len(value) == 0 {
		return 0, b != nil && !b.closed
	}
	written := 0
	for len(value) > 0 {
		if len(b.segments) == 0 || len(b.segments[len(b.segments)-1]) == cap(b.segments[len(b.segments)-1]) {
			reserve := int64(bufferSegmentBytes)
			if !b.budget.TryReserve(reserve) {
				return written, false
			}
			b.reserved += reserve
			b.segments = append(b.segments, make([]byte, 0, bufferSegmentBytes))
		}
		segment := b.segments[len(b.segments)-1]
		available := cap(segment) - len(segment)
		count := len(value)
		if count > available {
			count = available
		}
		segment = append(segment, value[:count]...)
		b.segments[len(b.segments)-1] = segment
		b.size += count
		written += count
		value = value[count:]
	}
	return written, true
}

func (b *SegmentedBuffer) Bytes() []byte {
	if b == nil || b.size == 0 {
		return nil
	}
	result := make([]byte, 0, b.size)
	for _, segment := range b.segments {
		result = append(result, segment...)
	}
	return result
}

func (b *SegmentedBuffer) Len() int {
	if b == nil {
		return 0
	}
	return b.size
}

func (b *SegmentedBuffer) Close() {
	if b == nil || b.closed {
		return
	}
	b.closed = true
	b.budget.Release(b.reserved)
	b.reserved = 0
	b.size = 0
	b.segments = nil
}
