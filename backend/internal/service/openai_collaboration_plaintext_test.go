package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const collaborationPlaintextTools = `[{"type":"namespace","name":"collaboration","description":"Collaboration tools","tools":[
{"type":"function","name":"spawn_agent","strict":true,"description":"Spawn a task","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true,"description":"Complete task"},"fork_turns":{"type":"string","enum":["all","none"]}},"required":["message"],"additionalProperties":false}},
{"type":"function","name":"followup_task","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true},"target":{"type":"string"}},"required":["message","target"]}},
{"type":"function","name":"send_message","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true},"target":{"type":"string"}},"required":["message","target"]}},
{"type":"function","name":"wait_agent","parameters":{"type":"object","properties":{"timeout_ms":{"type":"number"}}}}]}]`

func collaborationPlaintextAttempt() *openAICollaborationAttempt {
	return &openAICollaborationAttempt{policy: OpenAICollaborationSettings{Enabled: true, PlaintextEnabled: true},
		scope: "authenticated-test-scope", cache: &openAICollaborationCache{}, aliases: map[string]openAICollaborationTool{}}
}

func TestOpenAICollaborationPlaintextToolsPreserveSchemaAndHistory(t *testing.T) {
	t.Parallel()
	for _, carrier := range []string{"top", "additional", "both"} {
		t.Run(carrier, func(t *testing.T) {
			var tools []any
			require.NoError(t, json.Unmarshal([]byte(collaborationPlaintextTools), &tools))
			request := map[string]any{"tool_choice": map[string]any{"type": "function", "namespace": "collaboration", "name": "spawn_agent"},
				"input": []any{map[string]any{"type": "function_call", "namespace": "collaboration", "name": "spawn_agent", "id": "fc_keep", "call_id": "call_keep", "arguments": `{"message":"a task","fork_turns":"none"}`}}}
			if carrier != "additional" {
				request["tools"] = tools
			}
			if carrier != "top" {
				input, ok := request["input"].([]any)
				require.True(t, ok)
				request["input"] = append(input, map[string]any{"type": "additional_tools", "tools": tools})
			}
			body, err := json.Marshal(request)
			require.NoError(t, err)
			attempt := collaborationPlaintextAttempt()
			updated, err := prepareOpenAICollaborationBody(body, attempt)
			require.NoError(t, err)
			require.Len(t, attempt.aliases, 3)
			require.Equal(t, "collaboration__spawn_agent", gjson.GetBytes(updated, "input.0.name").String())
			require.False(t, gjson.GetBytes(updated, "input.0.namespace").Exists())
			require.Equal(t, gjson.GetBytes(body, "input.0.arguments").String(), gjson.GetBytes(updated, "input.0.arguments").String())
			require.Equal(t, "fc_keep", gjson.GetBytes(updated, "input.0.id").String())
			require.Equal(t, "call_keep", gjson.GetBytes(updated, "input.0.call_id").String())
			require.Equal(t, "collaboration__spawn_agent", gjson.GetBytes(updated, "tool_choice.name").String())
			path := "tools"
			if carrier == "additional" {
				path = "input.1.tools"
			}
			require.True(t, gjson.GetBytes(updated, path+".0.strict").Bool())
			require.False(t, gjson.GetBytes(updated, path+".0.parameters.properties.message.encrypted").Exists())
			require.Equal(t, "Complete task", gjson.GetBytes(updated, path+".0.parameters.properties.message.description").String())
			require.Equal(t, `["all","none"]`, gjson.GetBytes(updated, path+".0.parameters.properties.fork_turns.enum").Raw)
			require.Equal(t, "wait_agent", gjson.GetBytes(updated, path+".3.tools.0.name").String())
			again, err := prepareOpenAICollaborationBody(updated, attempt)
			require.NoError(t, err)
			require.Equal(t, updated, again, "conversion must be idempotent")
		})
	}
}

func TestOpenAICollaborationPlaintextUnsupportedChoiceAndSchemaRemainNative(t *testing.T) {
	t.Parallel()
	for _, choice := range []string{`{"type":"namespace","name":"collaboration"}`, `{"type":"unknown"}`, `"future-choice"`} {
		body := []byte(`{"tools":` + collaborationPlaintextTools + `,"tool_choice":` + choice + `}`)
		updated, err := prepareOpenAICollaborationBody(body, collaborationPlaintextAttempt())
		require.NoError(t, err)
		require.Equal(t, body, updated)
	}
	body := []byte(`{"tools":[{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"spawn_agent","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true,"$ref":"#/definitions/message"}}}}]}]}`)
	updated, err := prepareOpenAICollaborationBody(body, collaborationPlaintextAttempt())
	require.NoError(t, err)
	require.Equal(t, body, updated)
}

func TestOpenAICollaborationPlaintextAliasCollisionIsStable(t *testing.T) {
	t.Parallel()
	var request map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"tools":`+collaborationPlaintextTools+`}`), &request))
	requestTools, ok := request["tools"].([]any)
	require.True(t, ok)
	request["tools"] = append(requestTools, map[string]any{"type": "function", "name": "collaboration__spawn_agent", "parameters": map[string]any{"type": "object"}})
	body, err := json.Marshal(request)
	require.NoError(t, err)
	attempt := collaborationPlaintextAttempt()
	updated, err := prepareOpenAICollaborationBody(body, attempt)
	require.NoError(t, err)
	alias := gjson.GetBytes(updated, "tools.0.name").String()
	require.NotEqual(t, "collaboration__spawn_agent", alias)
	require.LessOrEqual(t, len(alias), 64)
	require.Equal(t, "collaboration__spawn_agent", gjson.GetBytes(updated, "tools.4.name").String())
	again, err := prepareOpenAICollaborationBody(body, attempt)
	require.NoError(t, err)
	require.Equal(t, alias, gjson.GetBytes(again, "tools.0.name").String())
}

func TestOpenAICollaborationPlaintextCachedAliasCannotClaimUserTool(t *testing.T) {
	t.Parallel()
	attempt := collaborationPlaintextAttempt()
	attempt.aliases["collaboration__send_message"] = openAICollaborationTool{Namespace: "collaboration", Name: "send_message"}
	body := []byte(`{"tools":[{"type":"function","name":"collaboration__send_message","parameters":{"type":"object","properties":{}}}]}`)
	updated, err := prepareOpenAICollaborationBody(body, attempt)
	require.NoError(t, err)
	require.Equal(t, body, updated)
	payload := []byte(`{"type":"response.output_item.done","item":{"type":"function_call","name":"collaboration__send_message","arguments":"{}"}}`)
	restored, err := restoreOpenAICollaborationPayload(payload, attempt)
	require.NoError(t, err)
	require.Equal(t, payload, restored)
}

func TestOpenAICollaborationPlaintextDelayedDeclarationUsesKnownMapping(t *testing.T) {
	t.Parallel()
	attempt := collaborationPlaintextAttempt()
	attempt.aliases["collaboration__send_message"] = openAICollaborationTool{Namespace: "collaboration", Name: "send_message"}
	body := []byte(`{"previous_response_id":"resp_keep","input":[{"type":"function_call","namespace":"collaboration","name":"send_message","call_id":"call_keep","arguments":"{}"}],"tool_choice":{"type":"function","namespace":"collaboration","name":"send_message"}}`)
	updated, err := prepareOpenAICollaborationBody(body, attempt)
	require.NoError(t, err)
	require.Equal(t, "collaboration__send_message", gjson.GetBytes(updated, "input.0.name").String())
	require.Equal(t, "collaboration__send_message", gjson.GetBytes(updated, "tool_choice.name").String())
	require.Equal(t, "resp_keep", gjson.GetBytes(updated, "previous_response_id").String())
	unknown, err := prepareOpenAICollaborationBody(body, collaborationPlaintextAttempt())
	require.NoError(t, err)
	require.Equal(t, body, unknown, "never guess an alias from the incoming name")
}

func TestOpenAICollaborationPlaintextDuplicateDeclarationsDoNotDuplicateAliases(t *testing.T) {
	t.Parallel()
	var tools []any
	require.NoError(t, json.Unmarshal([]byte(collaborationPlaintextTools), &tools))
	body, err := json.Marshal(map[string]any{"tools": append(tools, tools...)})
	require.NoError(t, err)
	updated, err := prepareOpenAICollaborationBody(body, collaborationPlaintextAttempt())
	require.NoError(t, err)
	count := 0
	for _, tool := range gjson.GetBytes(updated, "tools").Array() {
		if tool.Get("name").String() == "collaboration__spawn_agent" {
			count++
		}
	}
	require.Equal(t, 1, count)
}

func TestOpenAICollaborationPlaintextAgentMessagesKeepOpaqueAndUnknownBlocks(t *testing.T) {
	t.Parallel()
	body := []byte(`{"input":[
{"type":"agent_message","id":"amsg_text","author":"/root","recipient":"/root/child","content":[{"type":"input_text","text":"Message Type: NEW_TASK\nPayload:\n complete task\n"}]},
{"type":"agent_message","id":"amsg_opaque","content":[{"type":"input_text","text":"Payload:\n"},{"type":"encrypted_content","encrypted_content":"opaque-cipher"}]},
{"type":"agent_message","id":"amsg_media","content":[{"type":"input_text","text":"inspect attachment"},{"type":"future_attachment","payload":"retain-me"}]}]}`)
	for _, enabled := range []bool{true, false} {
		attempt := collaborationPlaintextAttempt()
		attempt.policy.Enabled = enabled
		updated, err := prepareOpenAICollaborationBody(body, attempt)
		require.NoError(t, err)
		require.Equal(t, "message", gjson.GetBytes(updated, "input.0.type").String())
		require.Equal(t, "user", gjson.GetBytes(updated, "input.0.role").String())
		require.Contains(t, gjson.GetBytes(updated, "input.0.content.0.text").String(), `recipient="/root/child"`)
		require.Equal(t, "Message Type: NEW_TASK\nPayload:\n complete task\n", gjson.GetBytes(updated, "input.0.content.1.text").String())
		require.JSONEq(t, gjson.GetBytes(body, "input.1").Raw, gjson.GetBytes(updated, "input.1").Raw)
		require.JSONEq(t, gjson.GetBytes(body, "input.2").Raw, gjson.GetBytes(updated, "input.2").Raw)
	}
}

func TestOpenAICollaborationPlaintextDisabledDoesNotGenerateAliases(t *testing.T) {
	t.Parallel()
	body := []byte(`{"tools":` + collaborationPlaintextTools + `,"tool_choice":"required"}`)
	attempt := collaborationPlaintextAttempt()
	attempt.policy.Enabled = false
	updated, err := prepareOpenAICollaborationBody(body, attempt)
	require.NoError(t, err)
	require.Equal(t, body, updated)
	require.Empty(t, attempt.aliases)
}

func TestOpenAICollaborationPlaintextResponseIdentityAndArguments(t *testing.T) {
	t.Parallel()
	attempt := collaborationPlaintextAttempt()
	_, err := prepareOpenAICollaborationBody([]byte(`{"tools":`+collaborationPlaintextTools+`}`), attempt)
	require.NoError(t, err)
	arguments := `{"message":"完整任务\nKeep spacing:  a  b","fork_turns":"none"}`
	for _, kind := range []string{"response.output_item.added", "response.output_item.done", "response.completed"} {
		item := map[string]any{"type": "function_call", "id": "fc_keep", "call_id": "call_keep", "name": "collaboration__spawn_agent", "arguments": arguments}
		event := map[string]any{"type": kind, "output_index": 2, "item": item}
		path := "item"
		if kind == "response.completed" {
			event = map[string]any{"type": kind, "response": map[string]any{"id": "resp_keep", "output": []any{item}}}
			path = "response.output.0"
		}
		payload, err := json.Marshal(event)
		require.NoError(t, err)
		restored, err := restoreOpenAICollaborationPayload(payload, attempt)
		require.NoError(t, err)
		require.Equal(t, "spawn_agent", gjson.GetBytes(restored, path+".name").String())
		require.Equal(t, "collaboration", gjson.GetBytes(restored, path+".namespace").String())
		require.Equal(t, arguments, gjson.GetBytes(restored, path+".arguments").String())
		require.Equal(t, "[]", gjson.GetBytes(restored, path+".encrypted_function_args").Raw)
		require.Equal(t, "call_keep", gjson.GetBytes(restored, path+".call_id").String())
	}
	delta := []byte(`{"type":"response.function_call_arguments.delta","item_id":"fc_keep","output_index":2,"name":"collaboration__spawn_agent","delta":"{\"message\":\"part"}`)
	restored, err := restoreOpenAICollaborationPayload(delta, attempt)
	require.NoError(t, err)
	require.Equal(t, gjson.GetBytes(delta, "delta").String(), gjson.GetBytes(restored, "delta").String())
	require.Equal(t, "spawn_agent", gjson.GetBytes(restored, "name").String())
}

func TestOpenAICollaborationPlaintextProofRequiresCompleteKnownCall(t *testing.T) {
	t.Parallel()
	attempt := collaborationPlaintextAttempt()
	attempt.aliases["collaboration__send_message"] = openAICollaborationTool{Namespace: "collaboration", Name: "send_message"}
	const plaintext = "Full plaintext task with no guessing"
	arguments, err := json.Marshal(map[string]string{"message": plaintext, "target": "/root/child"})
	require.NoError(t, err)
	call := fmt.Sprintf(`{"type":"function_call","name":"collaboration__send_message","arguments":%q}`, string(arguments))
	_, err = restoreOpenAICollaborationPayload([]byte(`{"type":"response.output_item.added","item":`+call+`}`), attempt)
	require.NoError(t, err)
	require.Empty(t, attempt.cache.read(attempt.scope).plaintext)
	_, err = restoreOpenAICollaborationPayload([]byte(`{"type":"response.output_item.done","item":`+call+`}`), attempt)
	require.NoError(t, err)
	entry := attempt.cache.read(attempt.scope)
	require.True(t, entry.plaintext[collaborationHash(plaintext)])
	require.Empty(t, attempt.cache.read("different-authenticated-scope").plaintext)
	child := &openAICollaborationAttempt{knownPlaintext: entry.plaintext}
	body := []byte(fmt.Sprintf(`{"input":[{"type":"agent_message","content":[{"type":"input_text","text":"Payload:\n"},{"type":"encrypted_content","encrypted_content":%q}]}]}`, plaintext))
	updated, err := prepareOpenAICollaborationBody(body, child)
	require.NoError(t, err)
	require.Equal(t, "message", gjson.GetBytes(updated, "input.0.type").String())
	require.Equal(t, plaintext, gjson.GetBytes(updated, "input.0.content.1.text").String())
	unknown, err := prepareOpenAICollaborationBody(body, &openAICollaborationAttempt{})
	require.NoError(t, err)
	require.Equal(t, body, unknown)
}

func TestOpenAICollaborationPlaintextNeverClearsCiphertextMarkers(t *testing.T) {
	t.Parallel()
	attempt := collaborationPlaintextAttempt()
	attempt.aliases["collaboration__send_message"] = openAICollaborationTool{Namespace: "collaboration", Name: "send_message"}
	for _, marker := range []string{`["message"]`, `"message"`, `true`} {
		payload := []byte(`{"type":"response.output_item.done","item":{"type":"function_call","name":"collaboration__send_message","encrypted_function_args":` + marker + `,"arguments":"{\"message\":\"cipher\"}"}}`)
		restored, err := restoreOpenAICollaborationPayload(payload, attempt)
		require.ErrorIs(t, err, errOpenAICollaborationCiphertextResponse)
		require.Nil(t, restored)
		require.Empty(t, attempt.knownPlaintext)
	}
	unknown := []byte(`{"type":"response.output_item.done","item":{"type":"function_call","name":"unrelated_tool","encrypted_function_args":["message"],"arguments":"opaque"}}`)
	restored, err := restoreOpenAICollaborationPayload(unknown, attempt)
	require.NoError(t, err)
	require.Equal(t, unknown, restored)
}

func TestOpenAICollaborationPlaintextCompletionWithoutToolAliases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		payload string
		want    bool
	}{
		{`{"type":"response.completed","response":{"status":"completed","error":null}}`, true},
		{`{"type":"response.completed","response":{"id":"resp_test"}}`, true},
		{`{"id":"resp_test","status":"completed","output":[]}`, true},
		{`{"type":"response.failed","response":{"status":"failed"}}`, false},
		{`{"type":"response.completed","response":{"status":"cancelled"}}`, false},
		{`{"type":"response.completed","response":{"error":{"code":"invalid_encrypted_content"}}}`, false},
	} {
		attempt := &openAICollaborationAttempt{}
		restored, err := restoreOpenAICollaborationPayload([]byte(tc.payload), attempt)
		require.NoError(t, err)
		require.Equal(t, tc.payload, string(restored))
		require.Equal(t, tc.want, attempt.completed)
	}
}

func TestOpenAICollaborationPlaintextBareErrorCanBeSupersededByCompletion(t *testing.T) {
	t.Parallel()
	attempt := &openAICollaborationAttempt{}
	_, err := restoreOpenAICollaborationPayload([]byte(`{"type":"error","error":{"code":"invalid_encrypted_content"}}`), attempt)
	require.NoError(t, err)
	require.True(t, attempt.businessFailed)
	require.False(t, attempt.completed)
	_, err = restoreOpenAICollaborationPayload([]byte(`{"type":"response.completed","response":{"status":"completed","error":null}}`), attempt)
	require.NoError(t, err)
	require.False(t, attempt.businessFailed)
	require.True(t, attempt.completed)
}

func TestOpenAICollaborationPlaintextQualifiedReferencesOnlyChangeInstructionFields(t *testing.T) {
	t.Parallel()
	tools := strings.Replace(collaborationPlaintextTools, "Spawn a task", "Call to=functions.collaboration.spawn_agent", 1)
	body := []byte(`{"instructions":"Use functions.collaboration.spawn_agent; keep functions.collaboration.spawn_agent_extra","tools":` + tools + `,"input":[{"type":"message","role":"developer","content":"Use collaboration.send_message"},{"type":"message","role":"user","content":"functions.collaboration.spawn_agent"},{"type":"function_call","namespace":"collaboration","name":"spawn_agent","arguments":"{\"message\":\"functions.collaboration.spawn_agent\"}"}]}`)
	updated, err := prepareOpenAICollaborationBody(body, collaborationPlaintextAttempt())
	require.NoError(t, err)
	require.Equal(t, "Use functions.collaboration__spawn_agent; keep functions.collaboration.spawn_agent_extra", gjson.GetBytes(updated, "instructions").String())
	require.Equal(t, "Call to=functions.collaboration__spawn_agent", gjson.GetBytes(updated, "tools.0.description").String())
	require.Equal(t, "Use collaboration__send_message", gjson.GetBytes(updated, "input.0.content").String())
	require.Equal(t, "functions.collaboration.spawn_agent", gjson.GetBytes(updated, "input.1.content").String())
	require.Equal(t, gjson.GetBytes(body, "input.2.arguments").String(), gjson.GetBytes(updated, "input.2.arguments").String())
}
