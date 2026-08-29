package conversationaudit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrUnsupportedPayload = errors.New("conversation audit payload has no supported readable content")

type ExtractResult struct {
	Payload CanonicalConversation
	Stats   CanonicalStats
	Reason  string
}

func ExtractRequest(protocol string, body []byte, maxBytes int) (ExtractResult, error) {
	var document any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		if isMediaOnlyProtocol(protocol) {
			payload := CanonicalConversation{
				Version:  CanonicalVersion,
				Messages: []Message{{Role: "user", Content: []ContentItem{{Type: "media_omitted"}}}},
			}
			limited, stats, limitErr := LimitCanonical(payload, PayloadSideRequest, maxBytes)
			return ExtractResult{Payload: limited, Stats: stats, Reason: "non_json_media_omitted"}, limitErr
		}
		return ExtractResult{}, fmt.Errorf("decode conversation audit request: %w", err)
	}

	root, _ := document.(map[string]any)
	payload := CanonicalConversation{Version: CanonicalVersion, Messages: []Message{}}
	switch normalizeProtocol(protocol) {
	case "openai_chat", "anthropic_messages":
		if system, ok := root["system"]; ok {
			payload.Messages = append(payload.Messages, messageFromValue("system", system)...)
		}
		payload.Messages = append(payload.Messages, messagesFromArray(root["messages"])...)
	case "openai_responses", "responses_websocket":
		container := root
		if response, ok := root["response"].(map[string]any); ok {
			container = response
		}
		if instructions, ok := container["instructions"]; ok {
			payload.Messages = append(payload.Messages, messageFromValue("system", instructions)...)
		}
		payload.Messages = append(payload.Messages, responseInputMessages(container["input"])...)
	case "gemini":
		if system, ok := root["systemInstruction"]; ok {
			payload.Messages = append(payload.Messages, geminiMessage("system", system)...)
		}
		if system, ok := root["system_instruction"]; ok {
			payload.Messages = append(payload.Messages, geminiMessage("system", system)...)
		}
		payload.Messages = append(payload.Messages, geminiContents(root["contents"])...)
		payload.Messages = append(payload.Messages, geminiContents(root["content"])...)
	case "embeddings":
		payload.Messages = append(payload.Messages, messageFromValue("user", root["input"])...)
	default:
		payload.Messages = append(payload.Messages, extractReadableOptions(root)...)
		if len(payload.Messages) == 0 {
			payload.Messages = append(payload.Messages, messagesFromArray(root["messages"])...)
		}
	}

	if len(payload.Messages) == 0 {
		return ExtractResult{Reason: "unsupported_payload"}, ErrUnsupportedPayload
	}
	limited, stats, err := LimitCanonical(payload, PayloadSideRequest, maxBytes)
	return ExtractResult{Payload: limited, Stats: stats}, err
}

func normalizeProtocol(protocol string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	switch protocol {
	case "openai_chat_completions", "chat_completions", "openai_chat":
		return "openai_chat"
	case "anthropic_messages", "claude_messages", "messages":
		return "anthropic_messages"
	case "openai_responses", "responses":
		return "openai_responses"
	case "responses_websocket":
		return "responses_websocket"
	case "gemini", "gemini_generate_content":
		return "gemini"
	case "embedding", "embeddings":
		return "embeddings"
	default:
		return protocol
	}
}

func messagesFromArray(value any) []Message {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]Message, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role := normalizeRole(stringValue(object["role"]))
		content := contentItems(object["content"])
		if call, ok := object["function_call"].(map[string]any); ok {
			content = append(content, toolCallItem(call))
		}
		if calls, ok := object["tool_calls"].([]any); ok {
			for _, call := range calls {
				if callObject, ok := call.(map[string]any); ok {
					if function, ok := callObject["function"].(map[string]any); ok {
						content = append(content, toolCallItem(function))
					}
				}
			}
		}
		if role != "" && len(content) > 0 {
			result = append(result, Message{Role: role, Content: content})
		}
	}
	return result
}

func responseInputMessages(value any) []Message {
	switch typed := value.(type) {
	case string:
		return []Message{{Role: "user", Content: []ContentItem{{Type: "text", Text: typed}}}}
	case map[string]any:
		return responseItemMessage(typed)
	case []any:
		result := make([]Message, 0, len(typed))
		for _, item := range typed {
			switch entry := item.(type) {
			case string:
				result = append(result, Message{Role: "user", Content: []ContentItem{{Type: "text", Text: entry}}})
			case map[string]any:
				result = append(result, responseItemMessage(entry)...)
			}
		}
		return result
	default:
		return nil
	}
}

func responseItemMessage(item map[string]any) []Message {
	itemType := strings.ToLower(stringValue(item["type"]))
	role := normalizeRole(stringValue(item["role"]))
	if role == "" {
		role = "user"
	}
	switch itemType {
	case "function_call":
		return []Message{{Role: "assistant", Content: []ContentItem{{
			Type: "tool_call", Name: stringValue(item["name"]), Arguments: jsonString(item["arguments"]),
		}}}}
	case "function_call_output":
		return []Message{{Role: "tool", Content: []ContentItem{{
			Type: "tool_result", Name: stringValue(item["name"]), Content: jsonString(item["output"]),
		}}}}
	default:
		content := contentItems(item["content"])
		if len(content) == 0 {
			content = contentItems(item)
		}
		if len(content) == 0 {
			return nil
		}
		return []Message{{Role: role, Content: content}}
	}
}

func geminiContents(value any) []Message {
	items, ok := value.([]any)
	if !ok {
		if object, objectOK := value.(map[string]any); objectOK {
			items = []any{object}
		} else {
			return nil
		}
	}
	result := make([]Message, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role := normalizeRole(stringValue(object["role"]))
		if role == "" {
			role = "user"
		}
		content := contentItems(object["parts"])
		if len(content) > 0 {
			result = append(result, Message{Role: role, Content: content})
		}
	}
	return result
}

func geminiMessage(role string, value any) []Message {
	if object, ok := value.(map[string]any); ok {
		items := contentItems(object["parts"])
		if len(items) > 0 {
			return []Message{{Role: role, Content: items}}
		}
	}
	return messageFromValue(role, value)
}

func messageFromValue(role string, value any) []Message {
	items := contentItems(value)
	if len(items) == 0 {
		return nil
	}
	return []Message{{Role: normalizeRole(role), Content: items}}
}

func contentItems(value any) []ContentItem {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []ContentItem{{Type: "text", Text: typed}}
	case []any:
		result := make([]ContentItem, 0, len(typed))
		for _, entry := range typed {
			result = append(result, contentItems(entry)...)
		}
		return result
	case map[string]any:
		typeName := strings.ToLower(stringValue(typed["type"]))
		switch typeName {
		case "text", "input_text", "output_text":
			if text := stringValue(typed["text"]); text != "" {
				return []ContentItem{{Type: "text", Text: text}}
			}
		case "tool_use", "tool_call", "function_call":
			return []ContentItem{{Type: "tool_call", Name: stringValue(typed["name"]), Arguments: jsonString(firstNonNil(typed["arguments"], typed["input"]))}}
		case "tool_result", "function_response", "function_call_output":
			return []ContentItem{{Type: "tool_result", Name: stringValue(typed["name"]), Content: jsonString(firstNonNil(typed["content"], typed["output"], typed["response"]))}}
		case "image", "image_url", "input_image", "audio", "input_audio", "video", "file", "input_file", "inline_data", "inlinedata":
			return []ContentItem{{Type: "media_omitted", MediaType: typeName, EncodedBytes: encodedMediaBytes(typed)}}
		}
		if function, ok := typed["functionCall"].(map[string]any); ok {
			return []ContentItem{toolCallItem(function)}
		}
		if response, ok := typed["functionResponse"].(map[string]any); ok {
			return []ContentItem{{Type: "tool_result", Name: stringValue(response["name"]), Content: jsonString(response["response"])}}
		}
		if text := stringValue(typed["text"]); text != "" {
			return []ContentItem{{Type: "text", Text: text}}
		}
		if hasMediaValue(typed) {
			return []ContentItem{{Type: "media_omitted", MediaType: typeName, EncodedBytes: encodedMediaBytes(typed)}}
		}
	}
	return nil
}

func extractReadableOptions(root map[string]any) []Message {
	if root == nil {
		return nil
	}
	keys := []string{"prompt", "input", "text", "query", "description", "lyrics", "negative_prompt", "instructions"}
	content := make([]ContentItem, 0, len(keys)+1)
	for _, key := range keys {
		value, ok := root[key]
		if !ok {
			continue
		}
		if key == "input" {
			if _, scalar := value.(string); !scalar {
				continue
			}
		}
		content = append(content, contentItems(value)...)
	}
	for key, value := range root {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "image") || strings.Contains(lower, "audio") || strings.Contains(lower, "video") || lower == "file" {
			if value != nil {
				content = append(content, ContentItem{Type: "media_omitted", MediaType: lower, EncodedBytes: encodedValueBytes(value)})
			}
		}
	}
	if len(content) == 0 {
		return nil
	}
	return []Message{{Role: "user", Content: content}}
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system", "developer", "user", "assistant", "tool":
		return strings.ToLower(strings.TrimSpace(role))
	case "model":
		return "assistant"
	case "function":
		return "tool"
	default:
		return ""
	}
}

func toolCallItem(call map[string]any) ContentItem {
	return ContentItem{Type: "tool_call", Name: stringValue(call["name"]), Arguments: jsonString(call["arguments"])}
}

func jsonString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func hasMediaValue(value map[string]any) bool {
	for key := range value {
		lower := strings.ToLower(key)
		if lower == "data" || lower == "url" || strings.Contains(lower, "image") || strings.Contains(lower, "audio") || strings.Contains(lower, "video") || strings.Contains(lower, "inline") {
			return true
		}
	}
	return false
}

func encodedMediaBytes(value map[string]any) int64 {
	for _, key := range []string{"data", "image", "audio", "video", "url"} {
		if candidate, ok := value[key]; ok {
			return encodedValueBytes(candidate)
		}
	}
	for _, nested := range value {
		if object, ok := nested.(map[string]any); ok {
			if size := encodedMediaBytes(object); size > 0 {
				return size
			}
		}
	}
	return 0
}

func encodedValueBytes(value any) int64 {
	text, ok := value.(string)
	if !ok {
		return 0
	}
	if comma := strings.IndexByte(text, ','); comma >= 0 && strings.Contains(strings.ToLower(text[:comma]), "base64") {
		text = text[comma+1:]
	}
	padding := int64(0)
	if strings.HasSuffix(text, "==") {
		padding = 2
	} else if strings.HasSuffix(text, "=") {
		padding = 1
	}
	return int64(len(text))*3/4 - padding
}

func isMediaOnlyProtocol(protocol string) bool {
	protocol = strings.ToLower(protocol)
	return strings.Contains(protocol, "stt") || strings.Contains(protocol, "audio") || strings.Contains(protocol, "multipart")
}
