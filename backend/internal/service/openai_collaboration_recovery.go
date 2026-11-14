package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const openAICollaborationMissingContentNotice = "Gateway compatibility notice: part of this agent message was omitted after an encrypted-content compatibility failure. Missing content does not mean the task was completed. Continue only with complete available instructions; if this work depends on the missing content, ask the parent agent to resend the complete task in plaintext."

type openAICollaborationOmission struct {
	MessageID    string
	InputIndex   int
	ContentIndex int
	Digest       string
}

type openAICollaborationRecovery struct {
	Body      []byte
	Omissions []openAICollaborationOmission
	Selection string
}

type openAICollaborationFailure struct {
	codes      []string
	messages   []string
	parameters []string
	blocked    bool
}

var (
	openAICollaborationInputPath   = regexp.MustCompile(`\binput(?:\[(\d+)\]|\.(\d+))(?:\.content(?:\[(\d+)\]|\.(\d+)))?(?:\.encrypted_content)?`)
	openAICollaborationPathHint    = regexp.MustCompile(`\binput\s*(?:\[|\.|/)`)
	openAICollaborationMessageID   = regexp.MustCompile(`\bamsg_[A-Za-z0-9_-]+\b`)
	errOpenAICollaborationLocation = errors.New("cannot safely locate encrypted agent content in the final upstream request")
)

// Recovery uses the final upstream request's indices. Callers must retain their
// origin map and independently enforce policy, replay safety and retry budget.
// A nil result is a non-match; a location error must never become bulk deletion.
func recoverOpenAICollaborationEncryptedContent(body []byte, upstreamStatus int, upstreamError []byte, allowUnlocated bool) (*openAICollaborationRecovery, error) {
	return recoverOpenAICollaborationEncryptedContentWithPlaintext(body, upstreamStatus, upstreamError, allowUnlocated, nil)
}

func recoverOpenAICollaborationEncryptedContentWithPlaintext(body []byte, upstreamStatus int, upstreamError []byte, allowUnlocated bool, knownPlaintext map[string]bool) (*openAICollaborationRecovery, error) {
	failure, matched := parseOpenAICollaborationFailure(upstreamStatus, upstreamError)
	if !matched {
		return nil, nil
	}
	var request map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &request); err != nil {
		return nil, fmt.Errorf("decode collaboration recovery request: %w", err)
	}
	input, ok := request["input"].([]any)
	if !ok {
		return nil, nil
	}
	// Preserve indices while protecting plaintext proven by an earlier adapted
	// tool call. A mixed message must not lose its new task in a bulk selection.
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || item["type"] != "agent_message" {
			continue
		}
		parts, _ := item["content"].([]any)
		for _, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok || part["type"] != "encrypted_content" {
				continue
			}
			text, ok := part["encrypted_content"].(string)
			if ok && text != "" && knownPlaintext[openAIEncryptedContentDigest(text)] {
				part["type"] = "input_text"
				part["text"] = text
				delete(part, "encrypted_content")
			}
		}
	}
	selected, selection, err := selectOpenAICollaborationOmissions(input, failure, allowUnlocated)
	if err != nil || len(selected) == 0 {
		return nil, err
	}
	applyOpenAICollaborationOmissions(input, selected)
	updated, err := marshalOpenAIUpstreamJSON(request)
	if err != nil {
		return nil, fmt.Errorf("encode collaboration recovery request: %w", err)
	}
	return &openAICollaborationRecovery{Body: updated, Omissions: selected, Selection: selection}, nil
}

func isOpenAICollaborationEncryptedContentError(status int, payload []byte) bool {
	_, matched := parseOpenAICollaborationFailure(status, payload)
	return matched
}

func parseOpenAICollaborationFailure(status int, payload []byte) (openAICollaborationFailure, bool) {
	var failure openAICollaborationFailure
	if (status != http.StatusOK && status != http.StatusBadRequest && status != http.StatusUnprocessableEntity) || len(payload) > 64*1024 {
		return failure, false
	}
	var root map[string]any
	if err := decodeOpenAIJSONUseNumber(payload, &root); err != nil {
		return failure, false
	}
	collectOpenAICollaborationFailure(root, 0, &failure)
	if failure.blocked {
		return failure, false
	}
	for _, message := range failure.messages {
		lower := strings.ToLower(message)
		for _, excluded := range []string{"content policy", "safety policy", "authentication failed", "rate limit", "too many requests"} {
			if strings.Contains(lower, excluded) {
				return failure, false
			}
		}
	}
	for _, code := range failure.codes {
		if code == "invalid_encrypted_content" {
			return failure, true
		}
	}
	for _, message := range failure.messages {
		lower := strings.ToLower(message)
		if openAICollaborationMessageID.MatchString(message) && strings.Contains(lower, "decrypt") {
			return failure, true
		}
		if strings.Contains(lower, "basispoints") && strings.Contains(lower, "cannot read") && strings.Contains(lower, "encrypted agent assignment") {
			return failure, true
		}
		if strings.Contains(lower, "encrypted function output") && (strings.Contains(lower, "decod") || strings.Contains(lower, "decrypt")) {
			return failure, true
		}
	}
	return failure, false
}

func collectOpenAICollaborationFailure(node map[string]any, depth int, failure *openAICollaborationFailure) {
	if depth > 5 {
		failure.blocked = true
		return
	}
	for _, field := range []string{"code", "type", "status", "status_code", "http_status"} {
		value := strings.ToLower(strings.TrimSpace(fmt.Sprint(node[field])))
		if value == "completed" || value == "response.completed" || value == "response.done" || value == "401" || value == "403" || value == "429" {
			failure.blocked = true
		}
		for _, excluded := range []string{"rate_limit", "authentication", "unauthorized", "forbidden", "permission", "invalid_api_key", "insufficient_quota", "policy", "safety"} {
			if strings.Contains(value, excluded) {
				failure.blocked = true
			}
		}
		if field == "code" {
			failure.codes = append(failure.codes, value)
		}
	}
	for _, field := range []string{"param", "path"} {
		if value, ok := node[field].(string); ok && strings.TrimSpace(value) != "" {
			failure.parameters = append(failure.parameters, value)
		}
	}
	for _, field := range []string{"message", "detail", "error"} {
		if value, ok := node[field].(string); ok && value != "" {
			failure.messages = append(failure.messages, value)
			if strings.HasPrefix(strings.TrimSpace(value), "{") {
				var wrapped map[string]any
				decoder := json.NewDecoder(strings.NewReader(value))
				decoder.UseNumber()
				if decoder.Decode(&wrapped) == nil {
					collectOpenAICollaborationFailure(wrapped, depth+1, failure)
				}
			}
		}
	}
	for _, field := range []string{"error", "response"} {
		if nested, ok := node[field].(map[string]any); ok {
			collectOpenAICollaborationFailure(nested, depth+1, failure)
		}
	}
}

func selectOpenAICollaborationOmissions(input []any, failure openAICollaborationFailure, allowUnlocated bool) ([]openAICollaborationOmission, string, error) {
	candidates := collectOpenAICollaborationOmissions(input)
	if len(candidates) == 0 {
		return nil, "", nil
	}
	paths := make([][]string, 0)
	ids := make(map[string]struct{})
	texts := append(append([]string(nil), failure.messages...), failure.parameters...)
	for _, text := range texts {
		matches := openAICollaborationInputPath.FindAllStringSubmatch(text, -1)
		if len(openAICollaborationPathHint.FindAllStringIndex(text, -1)) != len(matches) {
			return nil, "", errOpenAICollaborationLocation
		}
		for _, bounds := range openAICollaborationInputPath.FindAllStringIndex(text, -1) {
			suffix := text[bounds[1]:]
			if strings.HasPrefix(suffix, "[") || (len(suffix) > 1 && suffix[0] == '.' && ((suffix[1] >= 'a' && suffix[1] <= 'z') || suffix[1] == '_')) {
				return nil, "", errOpenAICollaborationLocation
			}
		}
		paths = append(paths, matches...)
		for _, id := range openAICollaborationMessageID.FindAllString(text, -1) {
			ids[id] = struct{}{}
		}
	}
	for _, parameter := range failure.parameters {
		if openAICollaborationInputPath.FindString(parameter) != strings.TrimSpace(parameter) {
			return nil, "", errOpenAICollaborationLocation
		}
	}
	selected := make(map[[2]int]struct{})
	selectedIDs := make(map[string]struct{})
	for _, path := range paths {
		inputIndex, err := strconv.Atoi(firstNonEmptyString(path[1], path[2]))
		if err != nil || inputIndex >= len(input) {
			return nil, "", errOpenAICollaborationLocation
		}
		item, ok := input[inputIndex].(map[string]any)
		if !ok {
			return nil, "", errOpenAICollaborationLocation
		}
		if itemType, _ := item["type"].(string); openAIEncryptedLineageItemType(itemType) {
			return nil, "", nil
		}
		contentIndex, err := strconv.Atoi(firstNonEmptyString(path[3], path[4]))
		if err != nil {
			return nil, "", errOpenAICollaborationLocation
		}
		matched := false
		for _, candidate := range candidates {
			if candidate.InputIndex == inputIndex && candidate.ContentIndex == contentIndex {
				selected[[2]int{inputIndex, contentIndex}] = struct{}{}
				selectedIDs[candidate.MessageID] = struct{}{}
				matched = true
			}
		}
		if !matched {
			return nil, "", errOpenAICollaborationLocation
		}
	}
	return finishOpenAICollaborationSelection(input, candidates, selected, selectedIDs, ids, len(paths) > 0, allowUnlocated)
}

func finishOpenAICollaborationSelection(input []any, candidates []openAICollaborationOmission, selected map[[2]int]struct{}, selectedIDs, ids map[string]struct{}, hasPaths, allowUnlocated bool) ([]openAICollaborationOmission, string, error) {
	selection := "exact_path"
	if !hasPaths {
		if len(ids) > 1 {
			return nil, "", errOpenAICollaborationLocation
		}
		selection = "message_id"
		if len(ids) == 0 {
			if !allowUnlocated {
				return nil, "", nil
			}
			selection = "unlocated_agent_set"
		}
		for _, candidate := range candidates {
			if _, match := ids[candidate.MessageID]; match || len(ids) == 0 {
				selected[[2]int{candidate.InputIndex, candidate.ContentIndex}] = struct{}{}
				selectedIDs[candidate.MessageID] = struct{}{}
			}
		}
	}
	for id := range ids {
		if _, matched := selectedIDs[id]; !matched {
			return nil, "", errOpenAICollaborationLocation
		}
	}
	for id := range selectedIDs {
		if id == "" {
			continue
		}
		count := 0
		for _, raw := range input {
			if item, ok := raw.(map[string]any); ok && item["id"] == id {
				count++
			}
		}
		if count != 1 {
			return nil, "", errOpenAICollaborationLocation
		}
	}
	var omissions []openAICollaborationOmission
	for _, candidate := range candidates {
		if _, matched := selected[[2]int{candidate.InputIndex, candidate.ContentIndex}]; matched {
			omissions = append(omissions, candidate)
		}
	}
	return omissions, selection, nil
}

func collectOpenAICollaborationOmissions(input []any) []openAICollaborationOmission {
	var candidates []openAICollaborationOmission
	for inputIndex, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || item["type"] != "agent_message" {
			continue
		}
		messageID, _ := item["id"].(string)
		content, _ := item["content"].([]any)
		for contentIndex, rawPart := range content {
			part, ok := rawPart.(map[string]any)
			if !ok || part["type"] != "encrypted_content" {
				continue
			}
			ciphertext, ok := part["encrypted_content"].(string)
			if ok && ciphertext != "" {
				candidates = append(candidates, openAICollaborationOmission{MessageID: messageID, InputIndex: inputIndex, ContentIndex: contentIndex, Digest: openAIEncryptedContentDigest(ciphertext)})
			}
		}
	}
	return candidates
}

func applyOpenAICollaborationOmissions(input []any, omissions []openAICollaborationOmission) {
	byMessage := make(map[int]map[int]struct{})
	for _, omission := range omissions {
		if byMessage[omission.InputIndex] == nil {
			byMessage[omission.InputIndex] = make(map[int]struct{})
		}
		byMessage[omission.InputIndex][omission.ContentIndex] = struct{}{}
	}
	for inputIndex, selected := range byMessage {
		item, ok := input[inputIndex].(map[string]any)
		if !ok {
			continue
		}
		content, ok := item["content"].([]any)
		if !ok {
			continue
		}
		remaining := make([]any, 0, len(content)+1)
		hasEncrypted := false
		for contentIndex, part := range content {
			if _, remove := selected[contentIndex]; remove {
				continue
			}
			remaining = append(remaining, part)
			if block, ok := part.(map[string]any); ok && block["type"] == "encrypted_content" {
				hasEncrypted = true
			}
		}
		messageID, _ := item["id"].(string)
		author, _ := item["author"].(string)
		recipient, _ := item["recipient"].(string)
		notice := fmt.Sprintf("%s\nOriginal agent envelope: id=%q; author=%q; recipient=%q.", openAICollaborationMissingContentNotice, messageID, author, recipient)
		remaining = append(remaining, map[string]any{"type": "input_text", "text": notice})
		if hasEncrypted {
			// Preserve the native shape until every remaining opaque block has an
			// explicitly authorized selection. Never present ciphertext as text.
			item["content"] = remaining
			continue
		}
		input[inputIndex] = map[string]any{"type": "message", "role": "user", "content": remaining}
	}
}
