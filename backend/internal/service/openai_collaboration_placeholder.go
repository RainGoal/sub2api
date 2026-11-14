package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// Only the single empty assistant text placeholder observed from the provider
// can extend the prelude. Unknown shapes commit the original stream unchanged.
type collaborationPlaceholder struct {
	responseID string
	itemID     string
	part       bool
	textDone   bool
	partDone   bool
	itemDone   bool
}

func (p *collaborationPlaceholder) responseMatches(payload []byte) bool {
	// gjson selects the first duplicate key while encoding/json selects the
	// last. Reject ambiguous envelopes before combining their interpretations.
	for _, object := range []gjson.Result{gjson.ParseBytes(payload), gjson.GetBytes(payload, "response")} {
		if object.Exists() && !collaborationUniqueObject(object) {
			return false
		}
	}
	id := gjson.GetBytes(payload, "response.id")
	if !id.Exists() {
		return true
	}
	if id.Type != gjson.String || id.String() == "" || (p.responseID != "" && id.String() != p.responseID) {
		return false
	}
	p.responseID = id.String()
	return true
}

func (p *collaborationPlaceholder) accept(kind string, payload []byte) bool {
	event := gjson.ParseBytes(payload)
	fields := "type sequence_number output_index "
	switch kind {
	case "response.output_item.added", "response.output_item.done":
		fields += "item"
	case "response.content_part.added", "response.content_part.done":
		fields += "item_id content_index part"
	case "response.output_text.delta":
		fields += "item_id content_index delta logprobs"
	case "response.output_text.done":
		fields += "item_id content_index text logprobs"
	default:
		return false
	}
	if !collaborationOnlyFields(event, fields) || event.Get("output_index").Raw != "0" {
		return false
	}
	if kind == "response.output_item.added" {
		item := event.Get("item")
		if p.itemID != "" || !collaborationBlankMessage(item) || len(item.Get("content").Array()) != 0 ||
			!collaborationOptionalStatus(item, "in_progress") {
			return false
		}
		p.itemID = item.Get("id").String()
		return true
	}
	if p.itemID == "" || p.itemDone {
		return false
	}
	if kind == "response.output_item.done" {
		item := event.Get("item")
		if !p.partDone || !collaborationBlankMessage(item) || item.Get("id").String() != p.itemID ||
			len(item.Get("content").Array()) != 1 || !collaborationOptionalStatus(item, "completed") {
			return false
		}
		p.itemDone = true
		return true
	}
	if event.Get("item_id").Type != gjson.String || event.Get("item_id").String() != p.itemID || event.Get("content_index").Raw != "0" {
		return false
	}
	switch kind {
	case "response.content_part.added":
		if p.part || !collaborationBlankTextPart(event.Get("part")) {
			return false
		}
		p.part = true
	case "response.output_text.delta":
		return p.part && !p.textDone && collaborationBlankText(event.Get("delta")) && collaborationEmptyOptionalArray(event.Get("logprobs"))
	case "response.output_text.done":
		if !p.part || p.textDone || !collaborationBlankText(event.Get("text")) || !collaborationEmptyOptionalArray(event.Get("logprobs")) {
			return false
		}
		p.textDone = true
	case "response.content_part.done":
		if !p.textDone || p.partDone || !collaborationBlankTextPart(event.Get("part")) {
			return false
		}
		p.partDone = true
	}
	return true
}

func (p *collaborationPlaceholder) hasOutput(payload []byte) bool {
	for _, path := range []string{"output", "response.output"} {
		output := gjson.GetBytes(payload, path)
		if !output.Exists() || output.Raw == "null" {
			continue
		}
		if !output.IsArray() {
			return true
		}
		items := output.Array()
		if len(items) == 0 {
			continue
		}
		if len(items) != 1 || p.itemID == "" || !collaborationBlankMessage(items[0]) || items[0].Get("id").String() != p.itemID {
			return true
		}
		if len(items[0].Get("content").Array()) != 0 && !p.part {
			return true
		}
	}
	return false
}

func collaborationBlankMessage(item gjson.Result) bool {
	if !collaborationOnlyFields(item, "id type role status content") || item.Get("type").String() != "message" ||
		item.Get("role").String() != "assistant" || item.Get("id").Type != gjson.String || item.Get("id").String() == "" {
		return false
	}
	if status := item.Get("status"); status.Exists() && status.String() != "in_progress" && status.String() != "completed" && status.String() != "incomplete" {
		return false
	}
	content := item.Get("content")
	if !content.IsArray() || len(content.Array()) > 1 {
		return false
	}
	for _, part := range content.Array() {
		if !collaborationBlankTextPart(part) {
			return false
		}
	}
	return true
}

func collaborationBlankTextPart(part gjson.Result) bool {
	return collaborationOnlyFields(part, "type text annotations logprobs") && part.Get("type").String() == "output_text" &&
		collaborationBlankText(part.Get("text")) && collaborationEmptyOptionalArray(part.Get("annotations")) &&
		collaborationEmptyOptionalArray(part.Get("logprobs"))
}

func collaborationBlankText(text gjson.Result) bool {
	return text.Type == gjson.String && strings.Trim(text.String(), " \t\r\n") == ""
}

func collaborationEmptyOptionalArray(value gjson.Result) bool {
	return !value.Exists() || (value.IsArray() && len(value.Array()) == 0)
}

func collaborationOptionalStatus(item gjson.Result, status string) bool {
	value := item.Get("status")
	return !value.Exists() || (value.Type == gjson.String && value.String() == status)
}

func collaborationOnlyFields(value gjson.Result, fields string) bool {
	if !value.IsObject() {
		return false
	}
	allowed := map[string]bool{}
	for _, field := range strings.Fields(fields) {
		allowed[field] = true
	}
	seen := map[string]bool{}
	valid := true
	value.ForEach(func(key, _ gjson.Result) bool {
		name := key.String()
		valid = !seen[name] && allowed[name]
		seen[name] = true
		return valid
	})
	return valid
}

func collaborationUniqueObject(value gjson.Result) bool {
	if !value.IsObject() {
		return false
	}
	seen := map[string]bool{}
	valid := true
	value.ForEach(func(key, _ gjson.Result) bool {
		name := key.String()
		valid = !seen[name]
		seen[name] = true
		return valid
	})
	return valid
}
