package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCollaborationProtocolForwardToolsAndResponseIdentity(t *testing.T) {
	for _, flatten := range []bool{false, true} {
		for _, additional := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("flatten=%v/additional=%v/stream=%v", flatten, additional, stream), func(t *testing.T) {
					var tools []any
					require.NoError(t, json.Unmarshal([]byte(collaborationPlaintextTools), &tools))
					// A user tool owns the usual flat name, so the converted tool must
					// have a distinct stable alias across declaration and response.
					tools = append(tools, map[string]any{"type": "function", "name": "collaboration__spawn_agent", "parameters": map[string]any{"type": "object"}})
					const userText = "Keep this literal: functions.collaboration.spawn_agent"
					request := map[string]any{"model": "gpt-5", "stream": stream,
						"instructions": "Call to=functions.collaboration.spawn_agent for the task.",
						"input": []any{
							map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "Use functions.collaboration.spawn_agent and collaboration.send_message directly."}}},
							map[string]any{"type": "message", "role": "user", "content": userText},
						},
						"tool_choice": map[string]any{"type": "function", "namespace": "collaboration", "name": "spawn_agent"}}
					if additional {
						input, ok := request["input"].([]any)
						require.True(t, ok)
						request["input"] = append(input, map[string]any{"type": "additional_tools", "role": "developer", "tools": tools})
					} else {
						request["tools"] = tools
					}
					body, err := json.Marshal(request)
					require.NoError(t, err)
					alias := "collaboration__spawn_agent_" + collaborationHash("collaboration:spawn_agent")[:12]
					arguments := `{"message":"Complete task; preserve functions.collaboration.spawn_agent literally","fork_turns":"none"}`
					response := collaborationProtocolResponse(t, alias, arguments, stream)
					upstream := &httpUpstreamRecorder{responses: []*http.Response{collaborationTestResponse(200, response, stream)}}
					svc := configureCollaborationTestService(t, upstream, true)
					account := newOpenAIRejectedFieldTestAccount()
					if flatten {
						account = newOpenAIOAuthNamespaceTestAccount()
						account.Extra = map[string]any{"openai_responses_flatten_namespaces": true}
					}
					c := newCollaborationTestContext(body, 11)
					if additional {
						c.Request.Header.Set(responsesLiteHeader, "true")
					}
					result, err := svc.Forward(context.Background(), c, account, body)
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Len(t, upstream.bodies, 1)
					forwarded := upstream.bodies[0]
					var decoded map[string]any
					require.NoError(t, json.Unmarshal(forwarded, &decoded))
					found := false
					for _, carrier := range collaborationToolCarriers(decoded) {
						declared, _ := carrier["tools"].([]any)
						for _, raw := range declared {
							tool := collaborationObject(raw)
							if tool["name"] == alias {
								found = true
								message, supported := collaborationEncryptedMessageSchema(tool)
								require.True(t, supported)
								require.NotContains(t, message, "encrypted")
							}
						}
					}
					require.True(t, found, "the actual final upstream body must declare the adapted tool")
					require.Contains(t, gjson.GetBytes(forwarded, "instructions").String(), "functions."+alias)
					require.Contains(t, gjson.GetBytes(forwarded, "input.0.content.0.text").String(), "functions."+alias)
					require.Equal(t, userText, gjson.GetBytes(forwarded, "input.1.content").String())
					require.Equal(t, alias, gjson.GetBytes(forwarded, "tool_choice.name").String())
					recorder, _ := c.Get("collaboration_test_recorder")
					recorded, ok := recorder.(*httptest.ResponseRecorder)
					require.True(t, ok)
					assertCollaborationProtocolClientResponse(t, recorded.Body.String(), arguments, stream)
				})
			}
		}
	}
}

func collaborationProtocolResponse(t *testing.T, alias, arguments string, stream bool) string {
	t.Helper()
	call := map[string]any{"id": "fc_adapted", "type": "function_call", "call_id": "call_adapted", "name": alias, "arguments": arguments, "status": "completed"}
	userCall := map[string]any{"id": "fc_user", "type": "function_call", "call_id": "call_user", "name": "collaboration__spawn_agent", "arguments": "{}", "status": "completed"}
	response := map[string]any{"id": "resp_protocol", "object": "response", "status": "completed", "model": "gpt-5", "output": []any{call, userCall}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5}}
	if !stream {
		encoded, err := json.Marshal(response)
		require.NoError(t, err)
		return string(encoded)
	}
	added := map[string]any{"id": "fc_adapted", "type": "function_call", "call_id": "call_adapted", "name": alias, "arguments": "", "status": "in_progress"}
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_protocol", "status": "in_progress", "output": []any{}}},
		{"type": "response.output_item.added", "output_index": 0, "item": added},
		{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "fc_adapted", "name": alias, "delta": arguments},
		{"type": "response.function_call_arguments.done", "output_index": 0, "item_id": "fc_adapted", "name": alias, "arguments": arguments},
		{"type": "response.output_item.done", "output_index": 0, "item": call},
		{"type": "response.completed", "response": response},
	}
	var result strings.Builder
	for _, event := range events {
		encoded, err := json.Marshal(event)
		require.NoError(t, err)
		fmt.Fprintf(&result, "event: %s\ndata: %s\n\n", event["type"], encoded)
	}
	return result.String()
}

func assertCollaborationProtocolClientResponse(t *testing.T, body, arguments string, stream bool) {
	t.Helper()
	assertCall := func(item gjson.Result) {
		require.Equal(t, "spawn_agent", item.Get("name").String())
		require.Equal(t, "collaboration", item.Get("namespace").String())
		require.Equal(t, "call_adapted", item.Get("call_id").String())
		require.Equal(t, "[]", item.Get("encrypted_function_args").Raw)
	}
	assertOutput := func(output gjson.Result) {
		assertCall(output.Get("0"))
		require.Equal(t, arguments, output.Get("0.arguments").String())
		require.Equal(t, "collaboration__spawn_agent", output.Get("1.name").String())
		require.False(t, output.Get("1.namespace").Exists(), "the ordinary user tool must not be claimed by collaboration")
	}
	if !stream {
		assertOutput(gjson.Get(body, "output"))
		return
	}
	completed := false
	for _, event := range strings.Split(body, "\n\n") {
		kind, payload := collaborationSSEEvent([]byte(event))
		switch kind {
		case "response.output_item.added", "response.output_item.done":
			assertCall(gjson.GetBytes(payload, "item"))
		case "response.function_call_arguments.delta":
			require.Equal(t, "spawn_agent", gjson.GetBytes(payload, "name").String())
			require.Equal(t, arguments, gjson.GetBytes(payload, "delta").String())
		case "response.function_call_arguments.done":
			require.Equal(t, "spawn_agent", gjson.GetBytes(payload, "name").String())
			require.Equal(t, arguments, gjson.GetBytes(payload, "arguments").String())
		case "response.completed":
			completed = true
			assertOutput(gjson.GetBytes(payload, "response.output"))
		}
	}
	require.True(t, completed)
}

func TestCollaborationProtocolForwardShutdownKeepsPlaintextHistoryReadable(t *testing.T) {
	body := []byte(`{"model":"gpt-5","stream":false,"tools":` + collaborationPlaintextTools + `,"input":[{"type":"agent_message","id":"amsg_plain","author":"/root","recipient":"/root/child","content":[{"type":"input_text","text":"Message Type: NEW_TASK\nPayload:\nCURRENT_TASK"}]}]}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{collaborationTestResponse(200, collaborationCompletedJSON, false)}}
	svc := configureCollaborationTestService(t, upstream, false)
	_, err := svc.Forward(context.Background(), newCollaborationTestContext(body, 11), newOpenAIRejectedFieldTestAccount(), body)
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 1)
	forwarded := upstream.bodies[0]
	require.Equal(t, "message", gjson.GetBytes(forwarded, "input.0.type").String())
	require.Equal(t, "user", gjson.GetBytes(forwarded, "input.0.role").String())
	require.Contains(t, string(forwarded), "CURRENT_TASK")
	require.True(t, gjson.GetBytes(forwarded, "tools.0.tools.0.parameters.properties.message.encrypted").Bool())
	require.NotContains(t, string(forwarded), "collaboration__spawn_agent")
}
