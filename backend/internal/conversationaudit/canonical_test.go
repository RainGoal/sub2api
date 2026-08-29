package conversationaudit

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestLimitCanonicalRequestPreservesSystemAndNewestMessages(t *testing.T) {
	payload := CanonicalConversation{Messages: []Message{
		{Role: "system", Content: []ContentItem{{Type: "text", Text: "system policy"}}},
		{Role: "user", Content: []ContentItem{{Type: "text", Text: strings.Repeat("old", 3000)}}},
		{Role: "assistant", Content: []ContentItem{{Type: "text", Text: strings.Repeat("history", 1000)}}},
		{Role: "user", Content: []ContentItem{{Type: "text", Text: "newest user message"}}},
	}}

	limited, stats, err := LimitCanonical(payload, PayloadSideRequest, MinimumPayloadMaxBytes)
	require.NoError(t, err)
	encoded, err := json.Marshal(limited)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), MinimumPayloadMaxBytes)
	require.True(t, stats.Truncated)
	require.Greater(t, limited.OmittedMessages, 0)
	require.Equal(t, "system", limited.Messages[0].Role)
	require.Equal(t, "newest user message", limited.Messages[len(limited.Messages)-1].Content[0].Text)
}

func TestLimitCanonicalDoesNotSplitUTF8(t *testing.T) {
	payload := CanonicalConversation{Messages: []Message{{
		Role: "user", Content: []ContentItem{{Type: "text", Text: strings.Repeat("中文😀é", 2000)}},
	}}}
	limited, _, err := LimitCanonical(payload, PayloadSideRequest, MinimumPayloadMaxBytes)
	require.NoError(t, err)
	encoded, err := json.Marshal(limited)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), MinimumPayloadMaxBytes)
	require.True(t, utf8.Valid(encoded))
	require.True(t, utf8.ValidString(limited.Messages[0].Content[0].Text))
}

func TestLimitCanonicalRejectsUnsafeCap(t *testing.T) {
	_, _, err := LimitCanonical(CanonicalConversation{}, PayloadSideRequest, MinimumPayloadMaxBytes-1)
	require.Error(t, err)
}
