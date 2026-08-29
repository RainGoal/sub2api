package conversationaudit

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemoryBudgetConcurrentReservationsStayBounded(t *testing.T) {
	budget := NewMemoryBudget(1024)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var accepted int64
	var mu sync.Mutex
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if budget.TryReserve(64) {
				mu.Lock()
				accepted += 64
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	require.Equal(t, int64(1024), accepted)
	require.Equal(t, int64(1024), budget.Used())
	require.False(t, budget.TryReserve(1))
	budget.Release(accepted)
	require.Zero(t, budget.Used())
}

func TestMemoryBudgetLowerLimitBlocksNewReservations(t *testing.T) {
	budget := NewMemoryBudget(1024)
	require.True(t, budget.TryReserve(800))
	budget.SetLimit(512)
	require.False(t, budget.TryReserve(1))
	budget.Release(400)
	require.True(t, budget.TryReserve(100))
}

func TestSegmentedBufferReleasesReservedSegments(t *testing.T) {
	budget := NewMemoryBudget(bufferSegmentBytes)
	buffer := NewSegmentedBuffer(budget)
	written, complete := buffer.Append(make([]byte, bufferSegmentBytes+1))
	require.Equal(t, bufferSegmentBytes, written)
	require.False(t, complete)
	require.Equal(t, int64(bufferSegmentBytes), budget.Used())
	require.Len(t, buffer.Bytes(), bufferSegmentBytes)
	buffer.Close()
	require.Zero(t, budget.Used())
	buffer.Close()
	require.Zero(t, budget.Used())
}
