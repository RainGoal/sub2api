package conversationaudit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateListFilterBoundsWindowsAndLimits(t *testing.T) {
	now := time.Now().UTC()
	require.NoError(t, validateListFilter(ListFilter{Start: now.Add(-24 * time.Hour), End: now, Limit: 100}))
	require.Error(t, validateListFilter(ListFilter{Start: now.Add(-32 * 24 * time.Hour), End: now}))
	require.Error(t, validateListFilter(ListFilter{Start: now.Add(-25 * time.Hour), End: now, Protocol: "openai"}))
	require.Error(t, validateListFilter(ListFilter{Start: now.Add(-time.Hour), End: now, Limit: 101}))
	require.Error(t, validateListFilter(ListFilter{End: now}))
}
