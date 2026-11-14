package service

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Matches the provider's synthetic production failure: only an assistant
// message containing one space precedes invalid_encrypted_content.
const collaborationProbeWhitespacePrelude = collaborationProbeCreated +
	`data: {"type":"response.in_progress","response":{"id":"resp_before","output":[]}}` + "\n\n" +
	`data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"content":[],"id":"msg_placeholder","role":"assistant","status":"in_progress","type":"message"}}` + "\n\n" +
	`data: {"type":"response.content_part.added","sequence_number":3,"output_index":0,"content_index":0,"item_id":"msg_placeholder","part":{"annotations":[],"text":"","type":"output_text"}}` + "\n\n" +
	`data: {"type":"response.output_text.delta","sequence_number":4,"output_index":0,"content_index":0,"item_id":"msg_placeholder","delta":" "}` + "\n\n" +
	`data: {"type":"response.output_text.done","sequence_number":5,"output_index":0,"content_index":0,"item_id":"msg_placeholder","text":" "}` + "\n\n" +
	`data: {"type":"response.content_part.done","sequence_number":6,"output_index":0,"content_index":0,"item_id":"msg_placeholder","part":{"annotations":[],"text":" ","type":"output_text"}}` + "\n\n" +
	`data: {"type":"response.output_item.done","sequence_number":7,"output_index":0,"item":{"content":[{"annotations":[],"text":" ","type":"output_text"}],"id":"msg_placeholder","role":"assistant","status":"completed","type":"message"}}` + "\n\n"

const collaborationProbePlaceholderFailed = `data: {"type":"response.failed","response":{"id":"resp_before","status":"failed","error":{"code":"invalid_encrypted_content","message":"Encrypted content could not be verified."}}}` + "\n\n"

func TestCollaborationProbeWhitespacePlaceholder(t *testing.T) {
	blankOutput := `"output":[{"id":"msg_placeholder","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":" ","annotations":[]}]}],`
	for _, tc := range []struct {
		name, body string
		failure    bool
	}{
		{"provider failure", collaborationProbeWhitespacePrelude + collaborationProbePlaceholderFailed, true},
		{"blank output snapshot", collaborationProbeWhitespacePrelude + strings.Replace(collaborationProbePlaceholderFailed, `"status":"failed",`, `"status":"failed",`+blankOutput, 1), true},
		{"normal completion", collaborationProbeWhitespacePrelude + collaborationProbeCompleted, false},
		{"no terminal", collaborationProbeWhitespacePrelude, false},
		{"visible delta", strings.Replace(collaborationProbeWhitespacePrelude, `"delta":" "`, `"delta":"hello"`, 1) + collaborationProbePlaceholderFailed, false},
		{"visible done", strings.Replace(collaborationProbeWhitespacePrelude, `"text":" "`, `"text":"hello"`, 1) + collaborationProbePlaceholderFailed, false},
		{"tool item", strings.Replace(collaborationProbeWhitespacePrelude, `"type":"message"`, `"type":"function_call"`, 1) + collaborationProbePlaceholderFailed, false},
		{"reasoning item", strings.Replace(collaborationProbeWhitespacePrelude, `"type":"message"`, `"type":"reasoning"`, 1) + collaborationProbePlaceholderFailed, false},
		{"annotations", strings.Replace(collaborationProbeWhitespacePrelude, `"annotations":[]`, `"annotations":[{"type":"url_citation"}]`, 1) + collaborationProbePlaceholderFailed, false},
		{"unknown item field", strings.Replace(collaborationProbeWhitespacePrelude, `"role":"assistant"`, `"role":"assistant","tool_call":{}`, 1) + collaborationProbePlaceholderFailed, false},
		{"unknown event field", strings.Replace(collaborationProbeWhitespacePrelude, `"delta":" "`, `"delta":" ","audio":"payload"`, 1) + collaborationProbePlaceholderFailed, false},
		{"compound unknown field", strings.Replace(collaborationProbeWhitespacePrelude, `"delta":" "`, `"delta":" ","type sequence_number":"payload"`, 1) + collaborationProbePlaceholderFailed, false},
		{"duplicate field", strings.Replace(collaborationProbeWhitespacePrelude, `"delta":" "`, `"delta":" ","delta":"result"`, 1) + collaborationProbePlaceholderFailed, false},
		{"wrong response identity", collaborationProbeWhitespacePrelude + strings.Replace(collaborationProbePlaceholderFailed, "resp_before", "resp_other", 1), false},
		{"duplicate response ID", collaborationProbeWhitespacePrelude + strings.Replace(collaborationProbePlaceholderFailed, `"id":"resp_before"`, `"id":"resp_before","id":"resp_other"`, 1), false},
		{"duplicate output hides tool", collaborationProbeWhitespacePrelude + strings.Replace(collaborationProbePlaceholderFailed, `"status":"failed",`, `"status":"failed",`+blankOutput+`"output":[{"type":"function_call"}],`, 1), false},
		{"duplicate response envelope", collaborationProbeWhitespacePrelude + strings.Replace(collaborationProbePlaceholderFailed, `"type":"response.failed",`, `"type":"response.failed","response":{"id":"resp_before"},`, 1), false},
		{"wrong item identity", strings.Replace(collaborationProbeWhitespacePrelude, `"item_id":"msg_placeholder"`, `"item_id":"msg_other"`, 1) + collaborationProbePlaceholderFailed, false},
		{"wrong content index", strings.Replace(collaborationProbeWhitespacePrelude, `"content_index":0`, `"content_index":1`, 1) + collaborationProbePlaceholderFailed, false},
		{"wrong snapshot identity", collaborationProbeWhitespacePrelude + strings.Replace(collaborationProbePlaceholderFailed, `"status":"failed",`, `"status":"failed",`+strings.Replace(blankOutput, "msg_placeholder", "msg_other", 1), 1), false},
		{"nonblank snapshot", collaborationProbeWhitespacePrelude + strings.Replace(collaborationProbePlaceholderFailed, `"status":"failed",`, `"status":"failed",`+strings.Replace(blankOutput, `"text":" "`, `"text":"result"`, 1), 1), false},
		{"snapshot without observed placeholder", strings.Replace(collaborationProbePlaceholderFailed, `"status":"failed",`, `"status":"failed",`+blankOutput, 1), false},
		{"orphan blank delta", `data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_placeholder","delta":" "}` + "\n\n" + collaborationProbePlaceholderFailed, false},
		{"bare error then completion", collaborationProbeWhitespacePrelude + collaborationProbeBareError + collaborationProbeCompleted, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := collaborationProbeResponse("text/event-stream", tc.body)
			defer func() { _ = resp.Body.Close() }()
			failure := probeOpenAICollaborationResponse(context.Background(), resp)
			require.Equal(t, tc.failure, len(failure) > 0)
			restored, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, tc.body, string(restored))
		})
	}
}

func TestCollaborationProbeFirstMeaningfulTextReleasesWhitespace(t *testing.T) {
	source, upstream := io.Pipe()
	resp := collaborationProbeResponse("text/event-stream", "")
	resp.Body = source
	defer func() { _ = resp.Body.Close(); _ = upstream.Close() }()
	boundary := strings.Index(collaborationProbeWhitespacePrelude, `data: {"type":"response.output_text.done"`)
	require.Positive(t, boundary)
	prefix := collaborationProbeWhitespacePrelude[:boundary] + `data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_placeholder","delta":"hello"}` + "\n\n"
	go func() { _, _ = io.WriteString(upstream, prefix) }()
	result := make(chan []byte, 1)
	go func() { result <- probeOpenAICollaborationResponse(context.Background(), resp) }()
	select {
	case failure := <-result:
		require.Empty(t, failure)
	case <-time.After(time.Second):
		t.Fatal("nonblank text must release the original stream without waiting for a terminal event")
	}
	go func() { _, _ = io.WriteString(upstream, collaborationProbePlaceholderFailed); _ = upstream.Close() }()
	restored, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, prefix+collaborationProbePlaceholderFailed, string(restored))
}
