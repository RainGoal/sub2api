package service

import (
	"context"

	"github.com/gin-gonic/gin"
)

// Native WebSocket connections may outlive the master switch, and a delayed
// plaintext agent message need not occur in their first frame. Keep this
// receive-only compatibility on every turn in a configured scope. It never
// lowers tools, rewrites calls, consults cached aliases or removes ciphertext.
func (s *OpenAIGatewayService) prepareOpenAICollaborationPlaintextHistory(ctx context.Context, c *gin.Context, account *Account, body []byte) ([]byte, error) {
	if !collaborationHasPlaintextAgentMessage(body) || len(s.collaborationPolicy(ctx, c, account).GroupIDs) == 0 {
		return body, nil
	}
	var request map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &request); err != nil {
		return nil, err
	}
	input, _ := request["input"].([]any)
	changed := false
	for index, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || item["type"] != "agent_message" {
			continue
		}
		if replacement, supported := plaintextOpenAICollaborationMessage(item, nil); supported {
			input[index] = replacement
			changed = true
		}
	}
	if !changed {
		return body, nil
	}
	return marshalOpenAIUpstreamJSON(request)
}
