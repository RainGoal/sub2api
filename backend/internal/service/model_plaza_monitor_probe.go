package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

func modelPlazaProbePlatformSupported(platform string) bool {
	switch platform {
	case PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax:
		return true
	default:
		return false
	}
}

func modelPlazaProbeSupported(model PlazaModel) bool {
	if !modelPlazaProbePlatformSupported(model.Platform) {
		return false
	}
	// A text greeting cannot attest to an image/video/embedding request path.
	if model.Pricing != nil && model.Pricing.BillingMode != "" && model.Pricing.BillingMode != BillingModeToken {
		return false
	}
	name := strings.ToLower(model.Name)
	for _, fragment := range []string{"embedding", "dall-e", "whisper", "tts-", "imagen", "veo-", "sora-", "gpt-image"} {
		if strings.Contains(name, fragment) {
			return false
		}
	}
	return true
}

// modelPlazaProbeRequest is deliberately independent of the legacy challenge
// checker. That checker adds arithmetic prompts, system text and a HEAD ping.
// Here each scheduled check emits exactly one request with the user input hi.
// Output caps are small and never increased or retried after an API rejection.
func modelPlazaProbeRequest(target ModelPlazaMonitorTarget) (string, map[string]any, error) {
	if !modelPlazaProbePlatformSupported(target.Platform) {
		return "", nil, fmt.Errorf("unsupported probe platform")
	}
	if target.Platform == PlatformAnthropic {
		return "/v1/messages", map[string]any{
			"model": target.Model, "max_tokens": 8,
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		}, nil
	}
	if target.Platform == PlatformGemini {
		return "/v1beta/models/" + url.PathEscape(target.Model) + ":generateContent", map[string]any{
			"contents":         []map[string]any{{"role": "user", "parts": []map[string]string{{"text": "hi"}}}},
			"generationConfig": map[string]any{"maxOutputTokens": 8},
		}, nil
	}
	if target.Platform == PlatformOpenAI && target.APIMode == MonitorAPIModeResponses {
		return "/v1/responses", map[string]any{
			"model": target.Model, "input": "hi", "max_output_tokens": 16, "stream": false,
		}, nil
	}
	body := map[string]any{
		"model": target.Model, "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 8, "stream": false,
	}
	if target.Platform == PlatformOpenAI {
		name := strings.ToLower(target.Model)
		if strings.HasPrefix(name, "o1") || strings.HasPrefix(name, "o3") || strings.HasPrefix(name, "o4") || strings.HasPrefix(name, "gpt-5") {
			delete(body, "max_tokens")
			body["max_completion_tokens"] = 16
			body["reasoning_effort"] = "low"
		}
	}
	return "/v1/chat/completions", body, nil
}

func (s *ModelPlazaMonitorService) probe(ctx context.Context, target ModelPlazaMonitorTarget, credential string) ModelPlazaMonitorCheck {
	result := ModelPlazaMonitorCheck{Status: "unknown", CheckedAt: s.now(), Reason: "probe_configuration"}
	path, body, err := modelPlazaProbeRequest(target)
	if err != nil {
		return result
	}
	data, err := json.Marshal(body)
	if err != nil {
		return result
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set(ChannelMonitorProbeHeaderName, BuildChannelMonitorProbeHeader(credential, s.now()))
	req.Header.Set(ModelPlazaMonitorProbeHeaderName, BuildChannelMonitorProbeHeader(credential, s.now()))
	if target.Platform == PlatformAnthropic {
		req.Header.Set("x-api-key", credential)
		req.Header.Set("anthropic-version", monitorAnthropicAPIVersion)
	}
	if target.Platform == PlatformGemini {
		req.Header.Set("x-goog-api-key", credential)
	}
	started := time.Now()
	recordLatency := func() int64 {
		result.CheckedAt = s.now()
		elapsed := time.Since(started).Milliseconds()
		result.LatencyMs = &elapsed
		return elapsed
	}
	resp, err := s.client.Do(req)
	if err != nil {
		recordLatency()
		result.Reason = "gateway_unreachable"
		result.ServiceFailure = true
		return result
	}
	defer func() { _ = resp.Body.Close() }()
	response, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	elapsed := recordLatency()
	if err != nil {
		result.Reason = "response_interrupted"
		result.ServiceFailure = true
		return result
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Reason, result.ServiceFailure = modelPlazaProbeFailure(resp.StatusCode, response)
		return result
	}
	if !modelPlazaProbeHasOutput(response, target.Platform, target.APIMode) {
		// An empty response can be a reasoning model consuming its tiny output
		// allowance. Do not mislabel it down or retry with a larger budget.
		result.Reason = "low_budget_response_empty"
		return result
	}
	result.Status = "operational"
	result.Reason = ""
	if elapsed > 6000 {
		result.Status = "degraded"
		result.Reason = "slow_response"
	}
	return result
}

func modelPlazaProbeHasOutput(body []byte, platform, apiMode string) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	switch platform {
	case PlatformAnthropic:
		for _, block := range gjson.GetBytes(body, "content").Array() {
			if block.Get("type").String() == "text" && strings.TrimSpace(block.Get("text").String()) != "" {
				return true
			}
		}
	case PlatformGemini:
		for _, part := range gjson.GetBytes(body, "candidates.0.content.parts").Array() {
			if strings.TrimSpace(part.Get("text").String()) != "" && !part.Get("thought").Bool() {
				return true
			}
		}
	default:
		if apiMode == MonitorAPIModeResponses {
			for _, item := range gjson.GetBytes(body, "output").Array() {
				for _, block := range item.Get("content").Array() {
					if block.Get("type").String() == "output_text" && strings.TrimSpace(block.Get("text").String()) != "" {
						return true
					}
				}
			}
			return false
		}
		return strings.TrimSpace(gjson.GetBytes(body, "choices.0.message.content").String()) != ""
	}
	return false
}

func modelPlazaProbeFailure(code int, body []byte) (string, bool) {
	if code == http.StatusUnauthorized || code == http.StatusForbidden || code == http.StatusPaymentRequired {
		return "probe_credentials_or_access", false
	}
	if code == http.StatusBadRequest || code == http.StatusUnprocessableEntity {
		return "probe_request_unsupported", false
	}
	if code == http.StatusTooManyRequests {
		message := strings.ToLower(gjson.GetBytes(body, "error.message").String() + " " + gjson.GetBytes(body, "error.type").String() + " " + gjson.GetBytes(body, "code").String())
		for _, fragment := range []string{"quota", "balance", "credit", "api_key_rate", "额度", "余额"} {
			if strings.Contains(message, fragment) {
				return "probe_budget_exhausted", false
			}
		}
		return "service_capacity", true
	}
	if code >= 500 || code == http.StatusRequestTimeout {
		return "service_unavailable", true
	}
	return "probe_request_unsupported", false
}
