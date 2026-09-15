package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
)

const (
	openAIClientContextWindowMessage = "Your input exceeds the context window of this model. Please adjust your input and try again."
	openAIClientCapacityMessage      = "Our servers are currently overloaded. Please try again later."
)

var openAIClientRetryDelayPattern = regexp.MustCompile(`(?i)\b(?:please\s+)?(?:try again in|retry after)\s+((?:[0-9]+(?:\.[0-9]+)?\s*(?:milliseconds?|seconds?|minutes?|hours?|ms|s|m|h)\s*)+)`)
var openAIClientResetDelayPattern = regexp.MustCompile(`(?i)\bresets in\s+((?:[0-9]+(?:\.[0-9]+)?\s*(?:milliseconds?|seconds?|minutes?|hours?|days?|weeks?|ms|s|m|h|d|w)\s*)+)`)
var openAIClientNumericMetadataPattern = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

// OpenAIClientErrorMessage is for the last downstream write only. Scheduling,
// accounting, and administrator diagnostics must continue to consume the
// original body/message. Even configured passthrough messages use this boundary.
func OpenAIClientErrorMessage(status int, body []byte, message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		message = extractOpenAISSEErrorMessage(body)
	}
	lower := strings.ToLower(message + " " + openAIClientErrorField(body, "message"))
	code := strings.ToLower(openAIClientErrorField(body, "code"))
	errType := strings.ToLower(openAIClientErrorField(body, "type"))
	if code == "cyber_policy" {
		return "Request blocked by upstream cyber-security policy"
	}
	if code == "policy_violation" {
		return "Request blocked by policy."
	}
	if code == "context_window_exceeded" || code == "input_too_long" || code == "input_too_large" || isOpenAIContextWindowError(message, body) {
		switch message {
		case "input exceeds the context window", "context window exceeded", "maximum context length exceeded":
			return message
		}
		return openAIClientContextWindowMessage
	}
	if code == "server_is_overloaded" || code == "slow_down" || isOpenAIRequestScopedCapacityShed(message, body) || strings.Contains(lower, "selected model is at capacity") {
		return openAIClientCapacityMessage
	}
	if status == http.StatusTooManyRequests || strings.Contains(code, "rate_limit") || strings.Contains(errType, "rate_limit") ||
		code == "usage_limit_reached" || errType == "usage_limit_reached" || errType == "gousagelimiterror" || strings.Contains(lower, "rate limit") {
		for _, candidate := range []string{message, openAIClientErrorField(body, "message")} {
			if delay := openAIClientRetryDelayPattern.FindStringSubmatch(candidate); len(delay) > 1 {
				return "Rate limit reached. Please try again in " + strings.TrimSpace(delay[1]) + "."
			}
			if delay := openAIClientResetDelayPattern.FindStringSubmatch(candidate); len(delay) > 1 {
				return "Usage limit reached. Resets in " + strings.TrimSpace(delay[1]) + "."
			}
		}
		return "Upstream rate limit exceeded, please retry later"
	}
	return openAIClientRequestErrorMessage(status, body, message, code, errType)
}

func openAIClientErrorField(body []byte, field string) string {
	for _, prefix := range []string{"error.", "response.error.", ""} {
		if value := gjson.GetBytes(body, prefix+field); value.Type == gjson.String && value.Str != "" {
			return strings.TrimSpace(value.Str)
		}
	}
	return ""
}

func openAIClientRequestErrorMessage(status int, body []byte, message, code, errType string) string {
	lower := strings.ToLower(message + " " + openAIClientErrorField(body, "message"))
	// These exact, identity-free diagnostics are existing client compatibility
	// contracts. Prefix/substring matches would allow appended provider details.
	switch message {
	case "Invalid 'input': expected an array.", "Instructions are required",
		openAIUpstreamClientErrorFallbackMessage, OpenAIRequestBodyTooLargeClientMessage:
		return message
	}
	switch {
	case strings.Contains(lower, "does not support image input"):
		return "The requested model does not support image input."
	case strings.Contains(lower, "image_generation requests require a responses-capable text model"):
		return "Image-generation requests require a Responses-capable text model."
	case code == "model_not_found" || strings.Contains(lower, "unknown provider for model") || strings.Contains(lower, "model not found"):
		return "The requested model is unavailable."
	case code == "previous_response_not_found" || code == "previous_response_id_not_found" || strings.Contains(lower, "previous response") && strings.Contains(lower, "not found"):
		return "Previous response not found. Retry with the full conversation input."
	case code == "invalid_encrypted_content" || strings.Contains(lower, "encrypted content") && strings.Contains(lower, "could not be verified"):
		return "Encrypted content could not be verified."
	case code == "upgrade_required":
		return "Upgrade required."
	case code == "websocket_not_supported" || code == "websocket_unsupported":
		return "WebSocket is unsupported."
	case code == "websocket_connection_limit_reached":
		return "WebSocket connection limit reached. Please retry later."
	case code == "content_policy_violation" || code == "content_policy" || code == "content_filter" || code == "safety_error" ||
		errType == "content_policy_violation" || errType == "content_policy" || errType == "content_filter" || errType == "safety_error" || errType == "invalid_prompt":
		return "The request was rejected by the safety policy."
	case code == "invalid_function_parameters" || code == "invalid_json_schema" || strings.Contains(lower, "invalid schema"):
		return "Invalid schema for function parameters."
	case (code == "" || code == "invalid_request_error" || code == "missing_required_parameter") && isOpenAIInstructionsRequiredError(http.StatusBadRequest, message, body):
		return "Instructions are required"
	case code == "missing_required_parameter" || strings.Contains(lower, "missing required parameter"):
		if param := openAIClientErrorParam(openAIClientErrorField(body, "param")); param != "" {
			return "Missing required parameter: '" + param + "'."
		}
		return "A required request parameter is missing."
	case code == "unsupported_parameter" || code == "unknown_parameter" || code == "unsupported_value" || strings.Contains(lower, "unsupported parameter"):
		return "The request contains an unsupported parameter or value."
	case code == "invalid_value" || code == "invalid_type":
		return "The request contains an invalid parameter value."
	case code == "request_timeout" || code == "timeout" || strings.Contains(lower, "timed out"):
		return "Upstream request timed out. Please retry."
	case errType == "invalid_request_error" || errType == "bad_request_error":
		return openAIUpstreamClientErrorFallbackMessage
	}
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return openAIUpstreamClientErrorFallbackMessage
	case http.StatusUnauthorized:
		return "Upstream authentication failed, please contact administrator"
	case http.StatusPaymentRequired:
		return "Upstream payment required: insufficient balance or billing issue"
	case http.StatusForbidden:
		return "Upstream access forbidden, please contact administrator"
	case http.StatusNotFound:
		return "The requested resource is unavailable."
	case http.StatusRequestEntityTooLarge:
		return OpenAIRequestBodyTooLargeClientMessage
	default:
		return "Upstream request failed"
	}
}

// Parameter names may otherwise embed a provider model or arbitrary diagnostics.
// Preserve the protocol paths clients use to locate malformed input/tool schemas.
var openAIClientParamPattern = regexp.MustCompile(`^(?:model|input|messages|instructions|tools|tool_choice|parallel_tool_calls|response_format|text|reasoning|temperature|top_p|max_tokens|max_output_tokens|stream|stream_options|store|metadata|previous_response_id|include|truncation|service_tier|seed|user|function_call|functions)(?:(?:\[[0-9]+\])|(?:\.(?:input|messages|content|tools|function|functions|parameters|properties|items|type|name|description|required|additionalProperties|format|schema|strict|text|reasoning|mode|effort|summary|status|role|tool_calls|arguments|tool_call_id|id|include_usage)))*$`)

func openAIClientErrorParam(param string) string {
	param = strings.TrimSpace(param)
	if len(param) <= 512 && openAIClientParamPattern.MatchString(param) {
		return param
	}
	return ""
}

// sanitizeOpenAIClientErrorPayload preserves response/output data and replaces
// only protocol diagnostics. RawMessage keeps opaque values and large usage
// numbers intact; decoding the envelope also removes ambiguous duplicate keys.
func sanitizeOpenAIClientErrorPayload(payload []byte, status int) ([]byte, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[DONE]")) {
		return payload, nil
	}
	if !json.Valid(trimmed) {
		return nil, errors.New("invalid upstream error JSON")
	}
	if trimmed[0] != '{' {
		if status >= 400 {
			return json.Marshal(map[string]json.RawMessage{"error": sanitizeOpenAIClientErrorObject(trimmed, status)})
		}
		return payload, nil
	}
	hasProtocolError := openAIClientHasErrorFields(gjson.ParseBytes(trimmed))
	if status < 400 && !hasProtocolError {
		return payload, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return nil, err
	}
	eventType := openAIClientRawString(envelope["type"])
	changed := hasProtocolError
	if raw, ok := envelope["error"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		envelope["error"] = sanitizeOpenAIClientErrorObject(raw, status)
		changed = true
	}
	if raw := bytes.TrimSpace(envelope["response"]); len(raw) > 0 && raw[0] == '{' {
		var response map[string]json.RawMessage
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, err
		}
		if rawError, ok := response["error"]; ok && !bytes.Equal(bytes.TrimSpace(rawError), []byte("null")) {
			response["error"] = sanitizeOpenAIClientErrorObject(rawError, status)
		}
		if openAIClientHasErrorFields(gjson.ParseBytes(raw)) {
			envelope["response"], _ = json.Marshal(response)
			changed = true
		}
	}
	// Some providers put code/message directly on an error event. Ordinary
	// message/delta objects must never be interpreted as diagnostics.
	if eventType == "error" || eventType == "response.failed" || status >= 400 {
		_, hasMessage := envelope["message"]
		if (len(envelope["error"]) == 0 || bytes.Equal(bytes.TrimSpace(envelope["error"]), []byte("null"))) && !hasMessage && len(envelope["response"]) == 0 {
			envelope["error"] = sanitizeOpenAIClientErrorObject(payload, status)
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(sanitizeOpenAIClientErrorObject(payload, status), &fields)
		for key := range envelope {
			if !openAIClientErrorEnvelopeField(key) {
				delete(envelope, key)
				continue
			}
			switch key {
			case "error":
				continue
			case "response", "usage":
				if !gjson.ParseBytes(envelope[key]).IsObject() {
					delete(envelope, key)
				}
				continue
			case "sequence_number":
				if gjson.ParseBytes(envelope[key]).Type != gjson.Number {
					delete(envelope, key)
				}
				continue
			case "type":
				if eventType == "error" || eventType == "response.failed" {
					continue
				}
			}
			if value, ok := fields[key]; ok {
				envelope[key] = value
			} else {
				delete(envelope, key)
			}
		}
		changed = true
	}
	if !changed {
		return payload, nil
	}
	return json.Marshal(envelope)
}

func openAIClientHasErrorFields(root gjson.Result) bool {
	found := false
	root.ForEach(func(key, value gjson.Result) bool {
		if key.Str == "type" && (value.Str == "error" || value.Str == "response.failed") {
			found = true
		}
		if key.Str == "error" && value.Type != gjson.Null {
			found = true
		}
		if key.Str == "response" && value.IsObject() {
			value.ForEach(func(key, value gjson.Result) bool {
				if key.Str == "error" && value.Type != gjson.Null {
					found = true
				}
				return !found
			})
		}
		return !found
	})
	return found
}

func sanitizeOpenAIClientErrorObject(raw []byte, status int) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	var fields map[string]json.RawMessage
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &fields)
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	if value := gjson.GetBytes(raw, "status_code"); value.Type == gjson.Number && value.Int() >= 400 && value.Int() <= 599 {
		status = int(value.Int())
	}
	message := openAIClientRawString(fields["message"])
	if message == "" && len(raw) > 0 && raw[0] == '"' {
		message = openAIClientRawString(raw)
	}
	clean := map[string]json.RawMessage{}
	for key, value := range fields {
		switch key {
		case "code", "type":
			if bytes.Equal(value, []byte("null")) || (key == "code" && gjson.ParseBytes(value).Type == gjson.Number) {
				clean[key] = value
			} else if token := openAIClientRawString(value); isOpenAIClientErrorToken(token) {
				clean[key] = value
			} else {
				clean[key] = json.RawMessage(`"upstream_error"`)
			}
		case "param":
			if bytes.Equal(value, []byte("null")) {
				clean[key] = value
			} else if param := openAIClientErrorParam(openAIClientRawString(value)); param != "" {
				clean[key], _ = json.Marshal(param)
			}
		case "status", "status_code", "retry_after", "retry_after_ms", "retry_after_seconds", "resets_at", "reset_at", "resets_in_seconds", "limit", "remaining":
			if valueType := gjson.ParseBytes(value).Type; valueType == gjson.Number || valueType == gjson.Null {
				clean[key] = value
			} else if valueType == gjson.String && openAIClientNumericMetadataPattern.MatchString(openAIClientRawString(value)) {
				clean[key] = value
			}
		case "request_id", "response_id", "event_id":
			if gjson.ParseBytes(value).Type == gjson.String {
				clean[key] = value
			}
		}
	}
	clean["message"], _ = json.Marshal(OpenAIClientErrorMessage(status, raw, message))
	result, _ := json.Marshal(clean)
	return result
}

func openAIClientRawString(value json.RawMessage) string {
	var text string
	_ = json.Unmarshal(value, &text)
	return text
}

func openAIClientErrorEnvelopeField(key string) bool {
	switch key {
	case "type", "error", "response", "usage", "message", "code", "param", "status", "status_code", "sequence_number", "event_id", "response_id", "request_id", "retry_after", "retry_after_ms", "retry_after_seconds", "resets_at", "reset_at", "resets_in_seconds":
		return true
	default:
		return false
	}
}

func isOpenAIClientErrorToken(token string) bool {
	if isOpenAIUpstreamAccessStateCode(token) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "error", "api_error", "server_error", "upstream_error", "internal_error", "internal_server_error",
		"invalid_request_error", "invalid_request", "invalid_prompt", "authentication_error", "authentication_failed",
		"permission_error", "permission_denied_error", "not_found_error", "bad_request_error", "rate_limit_error",
		"overloaded_error", "service_unavailable_error", "timeout_error", "request_timeout_error",
		"context_length_exceeded", "context_window_exceeded", "context_too_large", "input_too_long", "input_too_large",
		"server_is_overloaded", "slow_down", "rate_limit_exceeded", "usage_limit_reached", "gousagelimiterror", "insufficient_quota", "quota_exceeded",
		"token_limit_exceeded", "request_too_large", "request_body_too_large", "payload_too_large", "max_output_tokens",
		"content_policy_violation", "content_policy", "content_filter", "safety_error", "cyber_policy", "bio_policy", "misalignment_policy_violation", "policy_violation",
		"model_not_found", "model_not_supported", "model_specific_rejection", "unsupported_model", "data_residency_mismatch",
		"invalid_api_key", "api_key_disabled", "invalid_token", "access_token_invalid", "token_revoked", "token_invalidated", "token_invalid",
		"invalid_credentials", "credential_invalid", "unauthorized", "unauthorized_error", "forbidden", "access_denied", "permission_denied",
		"invalid_parameter", "invalid_parameters", "invalid_type", "invalid_value", "missing_required_parameter", "unsupported_parameter", "unknown_parameter", "unsupported_value",
		"invalid_function_parameters", "invalid_json_schema", "invalid_tool_call", "invalid_tool_result", "invalid_image", "invalid_image_url",
		"invalid_base64_image", "invalid_image_format", "invalid_image_mode", "image_too_large", "image_too_small", "image_parse_error", "image_content_policy_violation",
		"image_file_too_large", "unsupported_image_media_type", "empty_image_file", "failed_to_download_image", "image_file_not_found",
		"invalid_encrypted_content", "invalid_previous_response_id", "previous_response_id_not_found", "previous_response_not_found", "conversation_not_found", "response_not_found",
		"conversation_already_has_active_response", "response_in_progress", "websocket_connection_limit_reached", "connection_limit_exceeded",
		"unsupported_websocket_protocol", "upgrade_required", "websocket_not_supported", "websocket_unsupported", "session_expired", "cancelled", "canceled", "response_cancelled", "response_canceled",
		"billing_hard_limit_reached", "billing_not_active", "organization_restricted", "request_timeout", "timeout", "vector_store_timeout":
		return true
	default:
		return false
	}
}
