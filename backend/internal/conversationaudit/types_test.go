package conversationaudit

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNoopRecorderIsNilSafeAndAllocationFree(t *testing.T) {
	recorder := NoopRecorder()
	allocs := testing.AllocsPerRun(1000, func() {
		session := recorder.Begin(context.Background(), BeginInput{})
		session.Annotate(MetadataPatch{})
		session.SetRequest(CanonicalConversation{})
		session.Observe(ResponseEvent{})
		session.Finish(FinishResult{})
	})
	require.Zero(t, allocs)
}
