package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

// The wire shapes here were verified with Codex CLI 0.167.2: removing the
// message encryption annotation and returning encrypted_function_args: []
// produces input_text agent messages. Unknown shapes stay on the native path.
func prepareOpenAICollaborationBody(body []byte, attempt *openAICollaborationAttempt) ([]byte, error) {
	if attempt == nil {
		return body, nil
	}
	if attempt.preparedBodyHash != "" && attempt.preparedBodyHash == collaborationHash(string(body)) {
		return body, nil
	}
	var request map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &request); err != nil {
		return nil, fmt.Errorf("decode collaboration plaintext request: %w", err)
	}
	// A cached alias is not authority over a user-declared tool of the same
	// name. The exact output of this attempt is handled by the hash above.
	for _, carrier := range collaborationToolCarriers(request) {
		tools, _ := carrier["tools"].([]any)
		for _, raw := range tools {
			if tool, ok := raw.(map[string]any); ok && tool["type"] != "namespace" {
				if name, ok := tool["name"].(string); ok {
					delete(attempt.aliases, name)
				}
			}
		}
	}
	toolsChanged := false
	if attempt.policy.Enabled && attempt.policy.PlaintextEnabled && collaborationToolChoiceSupported(request["tool_choice"]) {
		toolsChanged = lowerOpenAICollaborationTools(request, attempt)
	}
	changed := toolsChanged
	rewriteHistory := toolsChanged || (len(attempt.aliases) > 0 && !hasNativeOpenAICollaborationTools(request) && collaborationToolChoiceSupported(request["tool_choice"]))
	if rewriteHistory {
		if choice, ok := request["tool_choice"].(map[string]any); ok && rewriteOpenAICollaborationCall(choice, attempt.aliases) {
			changed = true
		}
		if rewriteOpenAICollaborationReferences(request, attempt.aliases) {
			changed = true
		}
	}
	if input, ok := request["input"].([]any); ok {
		for index, raw := range input {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if item["type"] == "function_call" && rewriteHistory && rewriteOpenAICollaborationCall(item, attempt.aliases) {
				changed = true
			}
			if item["type"] == "agent_message" {
				if replacement, supported := plaintextOpenAICollaborationMessage(item, attempt); supported {
					input[index] = replacement
					changed = true
				}
			}
		}
	}
	if !changed {
		return body, nil
	}
	updated, err := marshalOpenAIUpstreamJSON(request)
	if err == nil {
		attempt.preparedBodyHash = collaborationHash(string(updated))
	}
	return updated, err
}

func collaborationToolChoiceSupported(choice any) bool {
	if choice == nil {
		return true
	}
	if value, ok := choice.(string); ok {
		return value == "auto" || value == "none" || value == "required"
	}
	value, ok := choice.(map[string]any)
	if !ok || value["type"] != "function" {
		return false
	}
	_, hasName := value["name"].(string)
	return hasName
}

func collaborationToolCarriers(request map[string]any) []map[string]any {
	carriers := []map[string]any{request}
	input, _ := request["input"].([]any)
	for _, raw := range input {
		if item, ok := raw.(map[string]any); ok && item["type"] == "additional_tools" {
			carriers = append(carriers, item)
		}
	}
	return carriers
}

func hasNativeOpenAICollaborationTools(request map[string]any) bool {
	for _, carrier := range collaborationToolCarriers(request) {
		tools, _ := carrier["tools"].([]any)
		for _, raw := range tools {
			if tool, ok := raw.(map[string]any); ok && tool["type"] == "namespace" && tool["name"] == "collaboration" {
				return true
			}
		}
	}
	return false
}

var collaborationQualifiedToolReference = regexp.MustCompile(`\b(?:functions\.)?collaboration\.(?:spawn_agent|followup_task|send_message)\b`)

func rewriteOpenAICollaborationReferences(request map[string]any, aliases map[string]openAICollaborationTool) bool {
	byName := make(map[string]string, len(aliases))
	for alias, identity := range aliases {
		byName[identity.Name] = alias
	}
	changed := false
	rewrite := func(value string) string {
		return collaborationQualifiedToolReference.ReplaceAllStringFunc(value, func(reference string) string {
			name := reference[strings.LastIndexByte(reference, '.')+1:]
			alias, found := byName[name]
			if !found {
				return reference
			}
			changed = true
			if strings.HasPrefix(reference, "functions.") {
				return "functions." + alias
			}
			return alias
		})
	}
	if instructions, ok := request["instructions"].(string); ok {
		request["instructions"] = rewrite(instructions)
	}
	input, _ := request["input"].([]any)
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || (item["role"] != "developer" && item["role"] != "system") || item["type"] == "agent_message" {
			continue
		}
		if content, ok := item["content"].(string); ok {
			item["content"] = rewrite(content)
		}
		parts, _ := item["content"].([]any)
		for _, rawPart := range parts {
			if part, ok := rawPart.(map[string]any); ok && (part["type"] == "input_text" || part["type"] == "text") {
				if text, ok := part["text"].(string); ok {
					part["text"] = rewrite(text)
				}
			}
		}
	}
	for _, carrier := range collaborationToolCarriers(request) {
		tools, _ := carrier["tools"].([]any)
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if description, ok := tool["description"].(string); ok {
				tool["description"] = rewrite(description)
			}
			if children, _, ok := collaborationNamespaceChildren(tool); ok {
				for _, rawChild := range children {
					if child, ok := rawChild.(map[string]any); ok {
						if description, ok := child["description"].(string); ok {
							child["description"] = rewrite(description)
						}
					}
				}
			}
		}
	}
	return changed
}

func lowerOpenAICollaborationTools(request map[string]any, attempt *openAICollaborationAttempt) bool {
	carriers := collaborationToolCarriers(request)
	occupied := make(map[string]bool)
	definitions := make(map[string]string)
	aliases := make(map[string]openAICollaborationTool)
	byName := make(map[string]string)
	for _, carrier := range carriers {
		tools, _ := carrier["tools"].([]any)
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if tool["type"] != "namespace" {
				if name, ok := tool["name"].(string); ok {
					occupied[name] = true
				}
			}
		}
	}
	for _, carrier := range carriers {
		tools, _ := carrier["tools"].([]any)
		for _, raw := range tools {
			namespace, ok := raw.(map[string]any)
			if !ok || namespace["type"] != "namespace" || namespace["name"] != "collaboration" {
				continue
			}
			children, _, supported := collaborationNamespaceChildren(namespace)
			if !supported {
				return false
			}
			for _, rawChild := range children {
				child, ok := rawChild.(map[string]any)
				if !ok || !collaborationTargetTool(child["name"]) {
					continue
				}
				message, valid := collaborationEncryptedMessageSchema(child)
				if !valid {
					return false
				}
				name, _ := child["name"].(string)
				encoded, err := marshalOpenAIUpstreamJSON(child)
				if err != nil || (definitions[name] != "" && definitions[name] != string(encoded)) {
					return false
				}
				definitions[name] = string(encoded)
				if message["encrypted"] != true {
					continue
				}
				alias := collaborationAliasForTool(name, attempt.aliases, occupied)
				if alias == "" {
					return false
				}
				byName[name] = alias
				aliases[alias] = openAICollaborationTool{Namespace: "collaboration", Name: name}
			}
		}
	}
	if len(aliases) == 0 {
		return false
	}
	for _, carrier := range carriers {
		lowerOpenAICollaborationCarrier(carrier, byName)
	}
	if choice, ok := request["tool_choice"].(map[string]any); ok {
		rewriteOpenAICollaborationCall(choice, aliases)
	}
	attempt.aliases = aliases
	if attempt.cache != nil {
		attempt.cache.update(attempt.scope, func(entry *openAICollaborationCacheEntry) {
			for alias, tool := range aliases {
				if len(entry.aliases) < 32 {
					entry.aliases[alias] = tool
				}
			}
		})
	}
	return true
}

func collaborationTargetTool(name any) bool {
	return name == "spawn_agent" || name == "followup_task" || name == "send_message"
}

func collaborationNamespaceChildren(namespace map[string]any) ([]any, string, bool) {
	children, hasTools := namespace["tools"]
	if other, hasChildren := namespace["children"]; hasChildren {
		if hasTools {
			return nil, "", false
		}
		result, ok := other.([]any)
		return result, "children", ok
	}
	result, ok := children.([]any)
	return result, "tools", ok
}

func collaborationEncryptedMessageSchema(tool map[string]any) (map[string]any, bool) {
	if tool["type"] != "function" {
		return nil, false
	}
	if strict, exists := tool["strict"]; exists {
		if _, ok := strict.(bool); !ok {
			return nil, false
		}
	}
	parameters, _ := tool["parameters"].(map[string]any)
	properties, _ := parameters["properties"].(map[string]any)
	message, _ := properties["message"].(map[string]any)
	if parameters["type"] != "object" || message["type"] != "string" {
		return nil, false
	}
	for _, schema := range []map[string]any{parameters, message} {
		for _, unsupported := range []string{"$ref", "anyOf", "oneOf", "allOf", "if", "then", "else"} {
			if _, exists := schema[unsupported]; exists {
				return nil, false
			}
		}
	}
	return message, true
}

func collaborationAliasForTool(name string, previous map[string]openAICollaborationTool, occupied map[string]bool) string {
	var known []string
	for alias, identity := range previous {
		if identity.Namespace == "collaboration" && identity.Name == name {
			known = append(known, alias)
		}
	}
	sort.Strings(known)
	if len(known) > 0 {
		if occupied[known[0]] {
			return ""
		}
		return known[0]
	}
	alias := "collaboration__" + name
	if occupied[alias] {
		alias += "_" + collaborationHash("collaboration:" + name)[:12]
	}
	if occupied[alias] || len(alias) > 64 {
		return ""
	}
	return alias
}

func lowerOpenAICollaborationCarrier(carrier map[string]any, byName map[string]string) {
	tools, ok := carrier["tools"].([]any)
	if !ok {
		return
	}
	output := make([]any, 0, len(tools))
	seen := make(map[string]bool)
	for _, raw := range tools {
		namespace, ok := raw.(map[string]any)
		if !ok || namespace["type"] != "namespace" || namespace["name"] != "collaboration" {
			output = append(output, raw)
			continue
		}
		children, field, _ := collaborationNamespaceChildren(namespace)
		remaining := make([]any, 0, len(children))
		for _, rawChild := range children {
			child, ok := rawChild.(map[string]any)
			name, _ := child["name"].(string)
			alias := byName[name]
			if !ok || alias == "" {
				remaining = append(remaining, rawChild)
				continue
			}
			if seen[alias] {
				continue
			}
			seen[alias] = true
			message, _ := collaborationEncryptedMessageSchema(child)
			delete(message, "encrypted")
			child["name"] = alias
			delete(child, "namespace")
			output = append(output, child)
		}
		if len(remaining) > 0 {
			namespace[field] = remaining
			output = append(output, namespace)
		}
	}
	carrier["tools"] = output
}

func rewriteOpenAICollaborationCall(call map[string]any, aliases map[string]openAICollaborationTool) bool {
	if call["namespace"] != "collaboration" {
		return false
	}
	for alias, identity := range aliases {
		if call["name"] == identity.Name {
			call["name"] = alias
			delete(call, "namespace")
			return true
		}
	}
	return false
}

func plaintextOpenAICollaborationMessage(item map[string]any, attempt *openAICollaborationAttempt) (map[string]any, bool) {
	parts, ok := item["content"].([]any)
	if !ok || len(parts) == 0 {
		return nil, false
	}
	content := make([]any, 0, len(parts)+1)
	id, _ := item["id"].(string)
	author, _ := item["author"].(string)
	recipient, _ := item["recipient"].(string)
	if id != "" || author != "" || recipient != "" {
		content = append(content, map[string]any{"type": "input_text", "text": fmt.Sprintf("Agent message envelope (metadata): id=%q; author=%q; recipient=%q.\n", id, author, recipient)})
	}
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			return nil, false
		}
		var text string
		switch part["type"] {
		case "input_text", "text":
			text, ok = part["text"].(string)
		case "encrypted_content":
			text, ok = part["encrypted_content"].(string)
			ok = ok && text != "" && attempt != nil && attempt.knownPlaintext[collaborationHash(text)]
		default:
			return nil, false
		}
		if !ok {
			return nil, false
		}
		content = append(content, map[string]any{"type": "input_text", "text": text})
	}
	return map[string]any{"type": "message", "role": "user", "content": content}, true
}

var errOpenAICollaborationCiphertextResponse = errors.New("collaboration plaintext tool returned encrypted arguments")

func restoreOpenAICollaborationPayload(payload []byte, attempt *openAICollaborationAttempt) ([]byte, error) {
	if attempt == nil || len(attempt.aliases) == 0 {
		observeOpenAICollaborationCompletion(payload, attempt)
		return payload, nil
	}
	var document map[string]any
	if err := decodeOpenAIJSONUseNumber(payload, &document); err != nil {
		return payload, nil
	}
	changed := false
	var proofs []string
	eventType, _ := document["type"].(string)
	canRecordProof := eventType == "response.output_item.done" || eventType == "response.function_call_arguments.done" ||
		eventType == "response.completed" || eventType == "response.done" || document["status"] == "completed"
	restore := func(node map[string]any) error {
		kind, _ := node["type"].(string)
		if kind != "function_call" && !strings.HasPrefix(kind, "response.function_call_arguments.") {
			return nil
		}
		name, _ := node["name"].(string)
		identity, found := attempt.aliases[name]
		if !found {
			return nil
		}
		if marker, exists := node["encrypted_function_args"]; exists && marker != nil {
			if list, ok := marker.([]any); !ok || len(list) != 0 {
				return errOpenAICollaborationCiphertextResponse
			}
		}
		node["name"] = identity.Name
		node["namespace"] = identity.Namespace
		if kind == "function_call" {
			node["encrypted_function_args"] = []any{}
		}
		changed = true
		if arguments, ok := node["arguments"].(string); ok && canRecordProof {
			var decoded map[string]any
			if json.Unmarshal([]byte(arguments), &decoded) == nil {
				if message, ok := decoded["message"].(string); ok && message != "" {
					proofs = append(proofs, collaborationHash(message))
				}
			}
		}
		return nil
	}
	if err := restore(document); err != nil {
		return nil, err
	}
	if item, ok := document["item"].(map[string]any); ok {
		if err := restore(item); err != nil {
			return nil, err
		}
	}
	for _, node := range []map[string]any{document, collaborationObject(document["response"])} {
		output, _ := node["output"].([]any)
		for _, raw := range output {
			if item, ok := raw.(map[string]any); ok {
				if err := restore(item); err != nil {
					return nil, err
				}
			}
		}
	}
	if !changed {
		observeOpenAICollaborationCompletion(payload, attempt)
		return payload, nil
	}
	restored, err := marshalOpenAIUpstreamJSON(document)
	if err == nil {
		rememberOpenAICollaborationPlaintext(attempt, proofs)
		observeOpenAICollaborationCompletion(payload, attempt)
	}
	return restored, err
}

func observeOpenAICollaborationCompletion(payload []byte, attempt *openAICollaborationAttempt) {
	if attempt == nil || !gjson.ValidBytes(payload) {
		return
	}
	kind := gjson.GetBytes(payload, "type").String()
	status := gjson.GetBytes(payload, "response.status").String()
	if kind == "error" || kind == "response.failed" || status == "failed" || gjson.GetBytes(payload, "status").String() == "failed" {
		attempt.businessFailed = true
		attempt.completed = false
		return
	}
	for _, path := range []string{"error", "response.error"} {
		if value := gjson.GetBytes(payload, path); value.Exists() && value.Type != gjson.Null {
			attempt.businessFailed = true
			attempt.completed = false
			return
		}
	}
	if kind == "response.completed" && (status == "" || status == "completed") {
		attempt.completed = true
		attempt.businessFailed = false
	} else if (kind == "response.done" && status == "completed") ||
		((kind == "" || kind == "response") && gjson.GetBytes(payload, "status").String() == "completed") {
		attempt.completed = true
		attempt.businessFailed = false
	}
}

func collaborationObject(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func rememberOpenAICollaborationPlaintext(attempt *openAICollaborationAttempt, proofs []string) {
	if len(proofs) == 0 {
		return
	}
	if attempt.knownPlaintext == nil {
		attempt.knownPlaintext = make(map[string]bool)
	}
	for _, digest := range proofs {
		if len(attempt.knownPlaintext) < 512 {
			attempt.knownPlaintext[digest] = true
		}
	}
	if attempt.cache != nil {
		attempt.cache.update(attempt.scope, func(entry *openAICollaborationCacheEntry) {
			for _, digest := range proofs {
				if len(entry.plaintext) < 512 {
					entry.plaintext[digest] = true
				}
			}
		})
	}
}
