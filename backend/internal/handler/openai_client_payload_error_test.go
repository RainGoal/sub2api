package handler

import (
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSClientPayloadFailureDoesNotPenalizeAccount(t *testing.T) {
	err := service.ValidateOpenAIClientModel("")
	require.Error(t, err)
	for _, wrapped := range []error{
		err,
		fmt.Errorf("write client frame: %w", err),
		service.NewOpenAIWSClientCloseError(coderws.StatusInternalError, "invalid response", err),
	} {
		require.False(t, shouldReportOpenAIWSProxyAccountFailure(wrapped))
	}
}
