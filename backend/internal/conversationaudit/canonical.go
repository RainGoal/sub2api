package conversationaudit

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

const MinimumPayloadMaxBytes = 4096

type CanonicalStats struct {
	OriginalBytes   int
	StoredBytes     int
	OmittedMessages int
	OmittedBytes    int64
	Truncated       bool
}

func LimitCanonical(input CanonicalConversation, side PayloadSide, maxBytes int) (CanonicalConversation, CanonicalStats, error) {
	if maxBytes < MinimumPayloadMaxBytes {
		return CanonicalConversation{}, CanonicalStats{}, errors.New("conversation audit payload cap is below minimum")
	}
	input.Version = CanonicalVersion
	if input.Messages == nil {
		input.Messages = []Message{}
	}
	normalizeCanonicalStrings(&input)
	original, err := json.Marshal(input)
	if err != nil {
		return CanonicalConversation{}, CanonicalStats{}, err
	}
	stats := CanonicalStats{OriginalBytes: len(original)}
	if len(original) <= maxBytes {
		stats.StoredBytes = len(original)
		return input, stats, nil
	}

	result := input
	result.Truncated = true
	for len(result.Messages) > 1 && canonicalJSONSize(result) > maxBytes {
		index := removableMessageIndex(result.Messages, side)
		stats.OmittedBytes += int64(canonicalJSONSize(CanonicalConversation{
			Version: CanonicalVersion, Messages: []Message{result.Messages[index]},
		}))
		result.Messages = append(result.Messages[:index], result.Messages[index+1:]...)
		stats.OmittedMessages++
	}
	result.OmittedMessages += stats.OmittedMessages
	result.OmittedBytes += stats.OmittedBytes

	for canonicalJSONSize(result) > maxBytes {
		target := largestCanonicalString(&result)
		if target == nil || len(*target) == 0 {
			break
		}
		over := canonicalJSONSize(result) - maxBytes
		removeBytes := over + 64
		if removeBytes >= len(*target) {
			stats.OmittedBytes += int64(len(*target))
			result.OmittedBytes += int64(len(*target))
			*target = ""
			continue
		}
		newLength := len(*target) - removeBytes
		shortened := truncateUTF8(*target, newLength)
		stats.OmittedBytes += int64(len(*target) - len(shortened))
		result.OmittedBytes += int64(len(*target) - len(shortened))
		*target = shortened
	}

	if canonicalJSONSize(result) > maxBytes {
		for _, message := range result.Messages {
			stats.OmittedBytes += int64(canonicalJSONSize(CanonicalConversation{
				Version: CanonicalVersion, Messages: []Message{message},
			}))
		}
		stats.OmittedMessages += len(result.Messages)
		result = CanonicalConversation{
			Version: CanonicalVersion, Messages: []Message{}, Truncated: true,
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return CanonicalConversation{}, CanonicalStats{}, err
	}
	if len(encoded) > maxBytes {
		return CanonicalConversation{}, CanonicalStats{}, errors.New("conversation audit minimal payload exceeds cap")
	}
	stats.StoredBytes = len(encoded)
	stats.Truncated = true
	return result, stats, nil
}

func removableMessageIndex(messages []Message, side PayloadSide) int {
	if side == PayloadSideResponse {
		return len(messages) / 2
	}
	for index, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "system" && role != "developer" {
			return index
		}
	}
	return 0
}

func normalizeCanonicalStrings(payload *CanonicalConversation) {
	for messageIndex := range payload.Messages {
		message := &payload.Messages[messageIndex]
		message.Role = strings.ToValidUTF8(message.Role, "")
		if message.Content == nil {
			message.Content = []ContentItem{}
		}
		for itemIndex := range message.Content {
			item := &message.Content[itemIndex]
			item.Type = strings.ToValidUTF8(item.Type, "")
			item.Text = strings.ToValidUTF8(item.Text, "")
			item.Name = strings.ToValidUTF8(item.Name, "")
			item.Arguments = strings.ToValidUTF8(item.Arguments, "")
			item.Content = strings.ToValidUTF8(item.Content, "")
			item.MediaType = strings.ToValidUTF8(item.MediaType, "")
			item.ResourceType = strings.ToValidUTF8(item.ResourceType, "")
			item.ID = strings.ToValidUTF8(item.ID, "")
			item.URL = strings.ToValidUTF8(item.URL, "")
		}
	}
	if payload.Error != nil {
		payload.Error.Code = strings.ToValidUTF8(payload.Error.Code, "")
		payload.Error.Message = strings.ToValidUTF8(payload.Error.Message, "")
	}
}

func largestCanonicalString(payload *CanonicalConversation) *string {
	var largest *string
	consider := func(value *string) {
		if value != nil && (largest == nil || len(*value) > len(*largest)) {
			largest = value
		}
	}
	for messageIndex := range payload.Messages {
		for itemIndex := range payload.Messages[messageIndex].Content {
			item := &payload.Messages[messageIndex].Content[itemIndex]
			consider(&item.Text)
			consider(&item.Arguments)
			consider(&item.Content)
		}
	}
	if payload.Error != nil {
		consider(&payload.Error.Message)
	}
	return largest
}

func canonicalJSONSize(payload CanonicalConversation) int {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return len(encoded)
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}
