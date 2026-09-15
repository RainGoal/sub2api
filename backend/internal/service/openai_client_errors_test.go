package service

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIClientErrorMessageKeepsRecoverySemantics(t *testing.T) {
	for _, tc := range []struct {
		name, body, message, want string
		status                    int
	}{
		{"context", `{"error":{"code":"context_length_exceeded"}}`, "Model internal-C has maximum context length 8192", openAIClientContextWindowMessage, 400},
		{"context_message_only", `{}`, "Your input exceeds the context window for internal-C", openAIClientContextWindowMessage, 502},
		{"capacity", `{"error":{"code":"server_is_overloaded"}}`, "internal-C server is overloaded", openAIClientCapacityMessage, 503},
		{"rate_delay", `{"error":{"code":"rate_limit_exceeded"}}`, "Rate limit reached for internal-C. Please try again in 467ms.", "Rate limit reached. Please try again in 467ms.", 429},
		{"rate_composite_delay", `{"error":{"type":"rate_limit_error"}}`, "internal-C limit. Please try again in 1m30.5s.", "Rate limit reached. Please try again in 1m30.5s.", 200},
		{"rate_delay_seconds", `{}`, "Rate limit for internal-C. Retry after 2 seconds.", "Rate limit reached. Please try again in 2 seconds.", 429},
		{"usage_reset_delay", `{"error":{"type":"GoUsageLimitError"}}`, "internal-C weekly usage limit reached. Resets in 2 days.", "Usage limit reached. Resets in 2 days.", 429},
		{"schema", `{"error":{"code":"invalid_function_parameters"}}`, "Invalid schema for function internal-C", "Invalid schema for function parameters.", 400},
		{"missing", `{"error":{"code":"missing_required_parameter","param":"input[8].tools[1].parameters"}}`, "internal-C missing required parameter", "Missing required parameter: 'input[8].tools[1].parameters'.", 400},
		{"missing_sensitive_param", `{"error":{"code":"missing_required_parameter","param":"internal-C"}}`, "internal-C rejected input", "A required request parameter is missing.", 400},
		{"model_not_found", `{"error":{"code":"model_not_found"}}`, "internal-C not found", "The requested model is unavailable.", 400},
		{"image_input", `{}`, `model "internal-C" does not support image input`, "The requested model does not support image input.", 400},
		{"image_only", `{}`, `/v1/responses image_generation requests require a Responses-capable text model; image-only model "internal-C" is not allowed`, "Image-generation requests require a Responses-capable text model.", 400},
		{"fast_policy", `{"error":{"code":"policy_violation"}}`, "priority is blocked for internal-C", "Request blocked by policy.", 403},
		{"cyber", `{"error":{"code":"cyber_policy"}}`, "internal-C blocked by cyber policy", "Request blocked by upstream cyber-security policy", 400},
		{"generic", `{}`, "internal-C processing detail", "Upstream request failed", 502},
		{"custom_rule", `{"error":{"code":"invalid_value"}}`, "Use model internal-C", "The request contains an invalid parameter value.", 418},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := OpenAIClientErrorMessage(tc.status, []byte(tc.body), tc.message)
			require.Equal(t, tc.want, got)
			require.NotContains(t, got, "internal-C")
		})
	}
	for _, message := range []string{openAIClientContextWindowMessage, openAIClientCapacityMessage, "Invalid 'input': expected an array."} {
		require.Equal(t, message, OpenAIClientErrorMessage(http.StatusBadRequest, nil, message))
	}
}

func TestSanitizeOpenAIClientErrorPayloadPreservesProtocolAndOriginal(t *testing.T) {
	payload := []byte(`{"type":"response.failed","sequence_number":9007199254740993,"response":{"id":"resp_1","model":"internal-C","usage":{"input_tokens":9007199254740993},"output":[{"type":"message","content":[{"text":"internal-C is literal answer text"}]}],"error":{"code":"context_length_exceeded","type":"invalid_request_error","message":"internal-C context window exceeded","param":"input[8].tools[1].parameters","request_id":"req_1","status_code":400,"diagnostics":{"model":"internal-C"}}}}`)
	original := bytes.Clone(payload)
	got, err := sanitizeOpenAIClientErrorPayload(payload, http.StatusOK)
	require.NoError(t, err)
	require.Equal(t, original, payload, "administrator diagnostics must retain the original bytes")
	require.Equal(t, "context_length_exceeded", gjson.GetBytes(got, "response.error.code").String())
	require.Equal(t, "invalid_request_error", gjson.GetBytes(got, "response.error.type").String())
	require.Equal(t, "input[8].tools[1].parameters", gjson.GetBytes(got, "response.error.param").String())
	require.Equal(t, "req_1", gjson.GetBytes(got, "response.error.request_id").String())
	require.Equal(t, int64(400), gjson.GetBytes(got, "response.error.status_code").Int())
	require.Equal(t, openAIClientContextWindowMessage, gjson.GetBytes(got, "response.error.message").String())
	require.False(t, gjson.GetBytes(got, "response.error.diagnostics").Exists())
	require.Equal(t, "9007199254740993", gjson.GetBytes(got, "sequence_number").Raw)
	require.Equal(t, "9007199254740993", gjson.GetBytes(got, "response.usage.input_tokens").Raw)
	require.Equal(t, gjson.GetBytes(payload, "response.output").Raw, gjson.GetBytes(got, "response.output").Raw)
}

func TestSanitizeOpenAIClientErrorPayloadClosesDiagnosticBypasses(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"model_not_found","type":"invalid_request_error","message":"internal-C absent","debug":"internal-C"},"detail":"internal-C","model":"internal-C"}`,
		`{"type":"error","code":"internal-C","message":"internal-C failed","param":"model.internal-C","detail":"internal-C"}`,
		`{"type":"internal-C","error":{"type":"internal-C","message":"internal-C failed"},"status":"internal-C"}`,
		`{"error":{"message":"safe","message":"internal-C"},"error":{"message":"internal-C"}}`,
		`{"type":"error","detail":{"model":"internal-C"}}`,
		`{"detail":"internal-C"}`,
		`{"error":"internal-C unavailable"}`,
		`"internal-C unavailable"`,
	} {
		got, err := sanitizeOpenAIClientErrorPayload([]byte(body), http.StatusBadRequest)
		require.NoError(t, err, body)
		require.True(t, gjson.ValidBytes(got), body)
		require.NotContains(t, string(got), "internal-C", body)
	}
}

func TestSanitizeOpenAIClientErrorPayloadKeepsRateLimitAndOpaqueIDs(t *testing.T) {
	body := []byte(`{"type":"error","event_id":"evt_1","request_id":"req_1","error":{"type":"usage_limit_reached","code":"rate_limit_exceeded","message":"Rate limit for internal-C. Please try again in 3s.","resets_at":1799999999,"retry_after_ms":3000,"param":null}}`)
	got, err := sanitizeOpenAIClientErrorPayload(body, http.StatusTooManyRequests)
	require.NoError(t, err)
	require.Equal(t, "usage_limit_reached", gjson.GetBytes(got, "error.type").String())
	require.Equal(t, "rate_limit_exceeded", gjson.GetBytes(got, "error.code").String())
	require.Equal(t, int64(1799999999), gjson.GetBytes(got, "error.resets_at").Int())
	require.Equal(t, int64(3000), gjson.GetBytes(got, "error.retry_after_ms").Int())
	require.Equal(t, "null", gjson.GetBytes(got, "error.param").Raw)
	require.Equal(t, "evt_1", gjson.GetBytes(got, "event_id").String())
	require.Equal(t, "req_1", gjson.GetBytes(got, "request_id").String())
	require.Equal(t, "Rate limit reached. Please try again in 3s.", gjson.GetBytes(got, "error.message").String())
	for _, metadata := range []string{`"resets_at":"1799999999"`, `"resets_in_seconds":133107`, `"resets_in_seconds":"133107"`} {
		input := []byte(`{"error":{"type":"usage_limit_reached","message":"internal-C limit",` + metadata + `}}`)
		got, err := sanitizeOpenAIClientErrorPayload(input, http.StatusTooManyRequests)
		require.NoError(t, err)
		before := parseOpenAIRateLimitResetTime(input)
		after := parseOpenAIRateLimitResetTime(got)
		require.NotNil(t, before)
		require.NotNil(t, after)
		require.InDelta(t, *before, *after, 1, "client reset-time parsing must remain usable")
	}
}

func TestSanitizeOpenAIClientErrorPayloadNoErrorIsBytePreserving(t *testing.T) {
	for _, body := range []string{
		"", "[DONE]", ": keepalive", `{"type":"response.output_text.delta","delta":"internal-C"}`,
		`{"type":"response.function_call_arguments.done","arguments":"{\"error\":{\"message\":\"internal-C\"}}"}`,
		`{"model":"internal-C","output":[],"error":null}`, `{"type":"message_start","message":{"content":[]}}`,
	} {
		got, err := sanitizeOpenAIClientErrorPayload([]byte(body), http.StatusOK)
		if body == ": keepalive" {
			require.Error(t, err, "SSE framing must be handled before invoking the JSON helper")
			continue
		}
		require.NoError(t, err, body)
		require.Equal(t, body, string(got))
	}
	got, err := sanitizeOpenAIClientErrorPayload([]byte(`{"type":"error","error":{"message":"internal-C"}`), http.StatusOK)
	require.Error(t, err)
	require.Nil(t, got, "malformed diagnostics must never be forwarded by a caller as a fallback")
}

func TestSanitizeOpenAIClientErrorPayloadDuplicateNullCannotBypass(t *testing.T) {
	for _, input := range []string{
		`{"error":{"message":"internal-C"},"error":null}`,
		`{"response":{"error":{"message":"internal-C"},"error":null}}`,
		`{"error":null,"error":{"message":"internal-C"}}`,
	} {
		got, err := sanitizeOpenAIClientErrorPayload([]byte(input), http.StatusOK)
		require.NoError(t, err)
		require.NotContains(t, string(got), "internal-C")
	}
}

func TestSanitizeOpenAIClientErrorPayloadPreservesWSRecoveryClassification(t *testing.T) {
	for _, code := range []string{"previous_response_not_found", "invalid_encrypted_content", "upgrade_required", "websocket_not_supported", "websocket_unsupported", "websocket_connection_limit_reached"} {
		input := []byte(`{"type":"error","error":{"type":"invalid_request_error","code":"` + code + `","message":"internal-C rejected continuation"}}`)
		wantReason, wantFallback := classifyOpenAIWSErrorEvent(input)
		got, err := sanitizeOpenAIClientErrorPayload(input, http.StatusBadRequest)
		require.NoError(t, err)
		require.Equal(t, code, gjson.GetBytes(got, "error.code").String())
		gotReason, gotFallback := classifyOpenAIWSErrorEvent(got)
		require.Equal(t, wantReason, gotReason, code)
		require.Equal(t, wantFallback, gotFallback, code)
		require.NotContains(t, string(got), "internal-C")
	}
}

func TestSanitizeOpenAIClientErrorPayloadKeepsPartialUsageAndSafetyClassification(t *testing.T) {
	const usage = `{"input_tokens":9007199254740993,"output_tokens":2,"total_tokens":9007199254740995,"input_tokens_details":{"cached_tokens":9007199254740991}}`
	for _, eventType := range []string{"error", "response.failed"} {
		for _, field := range []string{"code", "type"} {
			for _, token := range []string{"content_policy", "content_policy_violation", "safety_error"} {
				t.Run(eventType+"/"+field+"/"+token, func(t *testing.T) {
					input := []byte(`{"type":"` + eventType + `","usage":` + usage + `,"error":{"` + field + `":"` + token + `","message":"internal-C rejected the request"}}`)
					original := bytes.Clone(input)
					got, err := sanitizeOpenAIClientErrorPayload(input, http.StatusOK)
					require.NoError(t, err)
					require.Equal(t, usage, gjson.GetBytes(got, "usage").Raw)
					require.Equal(t, "9007199254740993", gjson.GetBytes(got, "usage.input_tokens").Raw)
					require.Equal(t, token, gjson.GetBytes(got, "error."+field).String())
					require.Equal(t, "The request was rejected by the safety policy.", extractOpenAISSEErrorMessage(got))
					require.NotContains(t, string(got), "internal-C")
					require.False(t, openAIStreamFailedEventShouldFailover(got, extractOpenAISSEErrorMessage(got)))
					require.False(t, openAIStreamErrorEventShouldFailover(got, extractOpenAISSEErrorMessage(got)))
					require.Equal(t, original, input)
				})
			}
		}
	}
	for _, usage := range []string{`"internal-C"`, `["internal-C"]`, `null`, `123`} {
		input := []byte(`{"type":"response.failed","usage":` + usage + `,"error":{"code":"safety_error","message":"internal-C"}}`)
		got, err := sanitizeOpenAIClientErrorPayload(input, http.StatusOK)
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(got, "usage").Exists())
		require.NotContains(t, string(got), "internal-C")
	}
}

func TestSanitizeOpenAIClientErrorPayloadPreservesKnownParameterDiagnostics(t *testing.T) {
	for _, tc := range []struct{ code, param string }{
		{"model_specific_rejection", "reasoning.mode"},
		{"unknown_parameter", "input[0].status"},
	} {
		input := []byte(`{"error":{"type":"invalid_request_error","code":"` + tc.code + `","param":"` + tc.param + `","message":"internal-C rejected this parameter"}}`)
		got, err := sanitizeOpenAIClientErrorPayload(input, http.StatusBadRequest)
		require.NoError(t, err)
		require.Equal(t, tc.code, gjson.GetBytes(got, "error.code").String())
		require.Equal(t, "invalid_request_error", gjson.GetBytes(got, "error.type").String())
		require.Equal(t, tc.param, gjson.GetBytes(got, "error.param").String())
		require.NotContains(t, string(got), "internal-C")
	}
	input := []byte(`{"error":{"type":"safety_error_internal-C","code":"model_specific_rejection_internal-C","param":"reasoning.internal-C","message":"internal-C"}}`)
	got, err := sanitizeOpenAIClientErrorPayload(input, http.StatusBadRequest)
	require.NoError(t, err)
	require.NotContains(t, string(got), "internal-C")
}

func TestSanitizeOpenAIClientErrorPayloadPreservesDocumentedResponseCodes(t *testing.T) {
	// ResponseError.code in the official Responses streaming-event schema.
	for _, code := range []string{
		"data_residency_mismatch", "bio_policy", "misalignment_policy_violation", "vector_store_timeout",
		"invalid_image_mode", "image_file_too_large", "unsupported_image_media_type", "empty_image_file",
		"failed_to_download_image", "image_file_not_found",
	} {
		t.Run(code, func(t *testing.T) {
			input := []byte(`{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"` + code + `","message":"internal-C rejected the request"}}}`)
			original := bytes.Clone(input)
			got, err := sanitizeOpenAIClientErrorPayload(input, http.StatusOK)
			require.NoError(t, err)
			require.Equal(t, code, gjson.GetBytes(got, "response.error.code").String())
			require.Equal(t, "failed", gjson.GetBytes(got, "response.status").String())
			require.NotEmpty(t, gjson.GetBytes(got, "response.error.message").String())
			require.NotContains(t, string(got), "internal-C")
			require.Equal(t, original, input)
		})
	}
}
