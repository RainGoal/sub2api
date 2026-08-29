package conversationaudit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ExtractResponse normalizes client-visible JSON or SSE output. Transport
// framing is consumed here and is never included in the stored payload.
func ExtractResponse(protocol string, body []byte, maxBytes int) (ExtractResult, error) {
	return ExtractResponseSegments(protocol, [][]byte{body}, maxBytes)
}

func ExtractResponseSegments(protocol string, segments [][]byte, maxBytes int) (ExtractResult, error) {
	payload := CanonicalConversation{Version: CanonicalVersion, Messages: []Message{}}
	if looksLikeSSESegments(segments) {
		extractSSEResponse(protocol, segments, &payload)
	} else if normalizeProtocol(protocol) == "responses_websocket" {
		if err := extractWebSocketResponse(protocol, segments, &payload); err != nil {
			return ExtractResult{Reason: "response_decode_failed"}, fmt.Errorf("decode conversation audit websocket response: %w", err)
		}
	} else {
		var document any
		decoder := json.NewDecoder(responseSegmentsReader(segments))
		decoder.UseNumber()
		if err := decoder.Decode(&document); err != nil {
			if isMediaOnlyProtocol(protocol) {
				payload.Messages = append(payload.Messages, Message{Role: "assistant", Content: []ContentItem{{Type: "media_omitted"}}})
			} else {
				return ExtractResult{Reason: "response_decode_failed"}, fmt.Errorf("decode conversation audit response: %w", err)
			}
		} else if root, ok := document.(map[string]any); ok {
			extractResponseDocument(protocol, root, &payload)
		}
	}
	if len(payload.Messages) == 0 && payload.Error == nil {
		return ExtractResult{Reason: "unsupported_response_payload"}, ErrUnsupportedPayload
	}
	limited, stats, err := LimitCanonical(payload, PayloadSideResponse, maxBytes)
	return ExtractResult{Payload: limited, Stats: stats}, err
}

func looksLikeSSESegments(segments [][]byte) bool {
	var prefix [64]byte
	written := 0
	for _, segment := range segments {
		if written == len(prefix) {
			break
		}
		written += copy(prefix[written:], segment)
	}
	trimmed := bytes.TrimSpace(prefix[:written])
	return bytes.HasPrefix(trimmed, []byte("data:")) || bytes.Contains(trimmed, []byte("\ndata:")) ||
		bytes.HasPrefix(trimmed, []byte("event:"))
}

func extractSSEResponse(protocol string, segments [][]byte, payload *CanonicalConversation) {
	stream := responseStreamAccumulator{items: make([]ContentItem, 0, 8)}
	scanner := bufio.NewScanner(responseSegmentsReader(segments))
	scanner.Buffer(make([]byte, 4096), MaxPayloadMaxBytes*2)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[len("data:"):])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		var root map[string]any
		if err := json.Unmarshal(data, &root); err != nil {
			continue
		}
		stream.add(protocol, root, payload)
	}
	stream.finish(payload)
}

func extractWebSocketResponse(protocol string, segments [][]byte, payload *CanonicalConversation) error {
	decoder := json.NewDecoder(responseSegmentsReader(segments))
	decoder.UseNumber()
	stream := responseStreamAccumulator{items: make([]ContentItem, 0, 8)}
	for {
		var root map[string]any
		if err := decoder.Decode(&root); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		stream.add(protocol, root, payload)
	}
	stream.finish(payload)
	return nil
}

type responseStreamAccumulator struct {
	text              strings.Builder
	items             []ContentItem
	userItems         []ContentItem
	sawTextDelta      bool
	audioEncodedBytes int64
}

func (a *responseStreamAccumulator) add(protocol string, root map[string]any, payload *CanonicalConversation) {
	if errValue := canonicalErrorFromValue(root["error"]); errValue != nil {
		payload.Error = errValue
	}
	eventType := strings.ToLower(stringValue(root["type"]))
	if strings.Contains(eventType, "reasoning") || strings.Contains(eventType, "thinking") {
		return
	}
	switch eventType {
	case "response.output_text.delta", "response.refusal.delta", "response.text.delta",
		"response.audio_transcript.delta", "response.output_audio_transcript.delta":
		if delta := stringValue(root["delta"]); delta != "" {
			a.text.WriteString(delta)
			a.sawTextDelta = true
		}
	case "response.output_text.done", "response.text.done", "response.audio_transcript.done",
		"response.output_audio_transcript.done":
		if !a.sawTextDelta {
			if text := stringValue(firstNonNil(root["text"], root["transcript"])); text != "" {
				a.text.WriteString(text)
			}
		}
	case "conversation.item.input_audio_transcription.completed", "conversation.item.input_audio_transcription.done",
		"input_audio_transcription.completed", "input_audio_transcription.done":
		if transcript := stringValue(firstNonNil(root["transcript"], root["text"])); transcript != "" {
			a.userItems = append(a.userItems, ContentItem{Type: "text", Text: transcript})
		}
	case "content_block_delta":
		if delta, ok := root["delta"].(map[string]any); ok {
			deltaType := strings.ToLower(stringValue(delta["type"]))
			if deltaType == "input_json_delta" {
				a.items = append(a.items, ContentItem{Type: "tool_call", Arguments: stringValue(delta["partial_json"])})
			} else if !strings.Contains(deltaType, "thinking") {
				a.text.WriteString(stringValue(delta["text"]))
			}
		}
	case "response.function_call_arguments.delta":
		if delta := stringValue(root["delta"]); delta != "" {
			a.items = append(a.items, ContentItem{Type: "tool_call", Arguments: delta, ID: stringValue(root["item_id"])})
		}
	case "response.output_item.done", "content_block_start":
		value := firstNonNil(root["item"], root["content_block"])
		a.items = append(a.items, responseOutputItems(value)...)
	case "response.completed", "response.failed", "response.incomplete", "response.done":
		if response, ok := root["response"].(map[string]any); ok {
			if payload.Error == nil {
				payload.Error = canonicalErrorFromValue(response["error"])
			}
			if !a.sawTextDelta {
				appendResponseRootMessages(protocol, response, payload)
			}
		}
	default:
		if strings.Contains(eventType, "audio") && !strings.Contains(eventType, "transcript") {
			a.audioEncodedBytes += encodedMediaBytes(root)
		} else {
			appendChatOrGeminiDelta(root, &a.text, &a.items)
		}
	}
}

func (a *responseStreamAccumulator) finish(payload *CanonicalConversation) {
	if len(a.userItems) > 0 {
		payload.Messages = append(payload.Messages, Message{Role: "user", Content: a.userItems})
	}
	if a.text.Len() > 0 {
		a.items = append([]ContentItem{{Type: "text", Text: a.text.String()}}, a.items...)
	}
	if a.audioEncodedBytes > 0 {
		a.items = append(a.items, ContentItem{
			Type: "media_omitted", MediaType: "audio", EncodedBytes: a.audioEncodedBytes,
		})
	}
	if len(a.items) > 0 {
		payload.Messages = append(payload.Messages, Message{Role: "assistant", Content: a.items})
	}
}

func responseSegmentsReader(segments [][]byte) io.Reader {
	readers := make([]io.Reader, 0, len(segments))
	for _, segment := range segments {
		readers = append(readers, bytes.NewReader(segment))
	}
	return io.MultiReader(readers...)
}

func appendChatOrGeminiDelta(root map[string]any, text *strings.Builder, items *[]ContentItem) {
	if choices, ok := root["choices"].([]any); ok {
		for _, choice := range choices {
			entry, _ := choice.(map[string]any)
			delta, _ := entry["delta"].(map[string]any)
			text.WriteString(stringValue(delta["content"]))
			if calls, ok := delta["tool_calls"].([]any); ok {
				for _, call := range calls {
					callObject, _ := call.(map[string]any)
					function, _ := callObject["function"].(map[string]any)
					*items = append(*items, ContentItem{
						Type: "tool_call", ID: stringValue(callObject["id"]),
						Name: stringValue(function["name"]), Arguments: stringValue(function["arguments"]),
					})
				}
			}
		}
	}
	if candidates, ok := root["candidates"].([]any); ok {
		for _, candidate := range candidates {
			entry, _ := candidate.(map[string]any)
			content, _ := entry["content"].(map[string]any)
			for _, item := range contentItems(content["parts"]) {
				if item.Type == "text" {
					text.WriteString(item.Text)
				}
			}
		}
	}
}

func extractResponseDocument(protocol string, root map[string]any, payload *CanonicalConversation) {
	payload.Error = canonicalErrorFromValue(root["error"])
	if payload.Error == nil {
		if message := stringValue(root["message"]); message != "" {
			payload.Error = &CanonicalError{
				Code:    jsonString(firstNonNil(root["code"], root["reason"], root["type"])),
				Message: message,
			}
		}
	}
	appendResponseRootMessages(protocol, root, payload)
	if len(payload.Messages) > 0 || payload.Error != nil {
		return
	}
	content := make([]ContentItem, 0, 4)
	for _, key := range []string{"text", "transcript", "output_text", "url"} {
		if value := stringValue(root[key]); value != "" {
			itemType := "text"
			item := ContentItem{Type: itemType, Text: value}
			if key == "url" {
				item = ContentItem{Type: "resource", URL: value}
			}
			content = append(content, item)
		}
	}
	content = append(content, responseDataItems(root["data"])...)
	if len(content) > 0 {
		payload.Messages = append(payload.Messages, Message{Role: "assistant", Content: content})
	}
}

func appendResponseRootMessages(protocol string, root map[string]any, payload *CanonicalConversation) {
	switch normalizeProtocol(protocol) {
	case "openai_chat":
		if choices, ok := root["choices"].([]any); ok {
			for _, choice := range choices {
				entry, _ := choice.(map[string]any)
				payload.Messages = append(payload.Messages, messagesFromArray([]any{entry["message"]})...)
			}
		}
	case "anthropic_messages":
		if items := contentItems(root["content"]); len(items) > 0 {
			payload.Messages = append(payload.Messages, Message{Role: "assistant", Content: items})
		}
	case "gemini":
		if candidates, ok := root["candidates"].([]any); ok {
			for _, candidate := range candidates {
				entry, _ := candidate.(map[string]any)
				messages := geminiContents(entry["content"])
				for index := range messages {
					messages[index].Role = "assistant"
				}
				payload.Messages = append(payload.Messages, messages...)
			}
		}
	default:
		payload.Messages = append(payload.Messages, responseOutputMessages(root["output"])...)
	}
	if len(payload.Messages) == 0 {
		payload.Messages = append(payload.Messages, responseOutputMessages(root["output"])...)
	}
	if len(payload.Messages) == 0 {
		if message, ok := root["message"].(map[string]any); ok {
			payload.Messages = append(payload.Messages, messagesFromArray([]any{message})...)
		}
	}
}

func responseOutputMessages(value any) []Message {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]Message, 0, len(items))
	for _, item := range items {
		object, _ := item.(map[string]any)
		if strings.Contains(strings.ToLower(stringValue(object["type"])), "reasoning") {
			continue
		}
		content := responseOutputItems(object)
		if len(content) > 0 {
			role := normalizeRole(stringValue(object["role"]))
			if role == "" {
				role = "assistant"
			}
			result = append(result, Message{Role: role, Content: content})
		}
	}
	return result
}

func responseOutputItems(value any) []ContentItem {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	typeName := strings.ToLower(stringValue(object["type"]))
	if strings.Contains(typeName, "reasoning") || strings.Contains(typeName, "thinking") {
		return nil
	}
	switch typeName {
	case "function_call", "tool_call":
		return []ContentItem{{Type: "tool_call", Name: stringValue(object["name"]), Arguments: jsonString(object["arguments"])}}
	case "function_call_output", "tool_result":
		return []ContentItem{{Type: "tool_result", Name: stringValue(object["name"]), Content: jsonString(firstNonNil(object["output"], object["content"]))}}
	}
	if items := contentItems(object["content"]); len(items) > 0 {
		return items
	}
	return contentItems(object)
}

func responseDataItems(value any) []ContentItem {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]ContentItem, 0, len(items))
	for _, item := range items {
		object, _ := item.(map[string]any)
		switch {
		case object["embedding"] != nil:
			result = append(result, ContentItem{Type: "vector_omitted"})
		case stringValue(object["url"]) != "":
			result = append(result, ContentItem{Type: "resource", URL: stringValue(object["url"])})
		case stringValue(object["b64_json"]) != "":
			result = append(result, ContentItem{Type: "media_omitted", MediaType: "image", EncodedBytes: encodedValueBytes(object["b64_json"])})
		}
	}
	return result
}

func canonicalErrorFromValue(value any) *CanonicalError {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return &CanonicalError{Message: typed}
		}
	case map[string]any:
		message := stringValue(firstNonNil(typed["message"], typed["detail"], typed["error"]))
		code := jsonString(firstNonNil(typed["code"], typed["type"], typed["status"]))
		if message != "" || code != "" {
			return &CanonicalError{Code: code, Message: message}
		}
	}
	return nil
}
