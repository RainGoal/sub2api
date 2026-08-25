package videoprovider

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func cloneObject(source map[string]any) (map[string]any, error) {
	body, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var clone map[string]any
	if err := json.Unmarshal(body, &clone); err != nil {
		return nil, err
	}
	if clone == nil {
		clone = make(map[string]any)
	}
	return clone, nil
}

func parseTaskBody(body []byte, fallbackTaskID string, idPaths ...string) (Task, map[string]any, error) {
	var payload map[string]any
	if len(body) == 0 || json.Unmarshal(body, &payload) != nil || payload == nil {
		return Task{}, nil, fmt.Errorf("video provider returned invalid JSON")
	}
	task := Task{ID: firstPathText(payload, idPaths...)}
	if task.ID == "" {
		task.ID = strings.TrimSpace(fallbackTaskID)
	}
	task.Model = firstPathText(payload, "model", "data.model", "properties.origin_model_name", "properties.upstream_model_name")
	if canonical, ok := CanonicalModel(task.Model); ok {
		task.Model = canonical
	}
	task.Resolution = canonicalResolution(firstPathText(payload, "resolution", "data.resolution", "video.resolution", "properties.resolution"))
	task.DurationSeconds = firstPathInt(payload, "duration", "data.duration", "video.duration", "properties.duration")
	task.ReferenceInputSeconds = firstPathInt(payload,
		"usage.reference_video_seconds", "usage.reference_video_input_seconds", "usage.reference_video_duration",
		"usage.input_video_seconds", "usage.input_video_duration", "billing.reference_video_seconds",
		"billing.reference_video_duration_seconds", "data.usage.reference_video_seconds",
		"data.usage.reference_video_input_seconds", "properties.reference_video_duration")
	return task, payload, nil
}

func parseOpenAIModelIDs(body []byte, supports func(string) bool) ([]string, error) {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if len(body) == 0 || json.Unmarshal(body, &payload) != nil {
		return nil, fmt.Errorf("video provider returned an invalid model list")
	}
	seen := make(map[string]struct{}, len(payload.Data))
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		model := strings.TrimSpace(item.ID)
		key := strings.ToLower(model)
		if model == "" || (supports != nil && !supports(model)) {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}

func firstPathText(payload map[string]any, paths ...string) string {
	for _, path := range paths {
		if value, ok := pathValue(payload, path).(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPathInt(payload map[string]any, paths ...string) int {
	for _, path := range paths {
		switch value := pathValue(payload, path).(type) {
		case float64:
			if value > 0 {
				return int(value)
			}
		case json.Number:
			if parsed, err := value.Int64(); err == nil && parsed > 0 {
				return int(parsed)
			}
		}
	}
	return 0
}

func pathValue(payload map[string]any, path string) any {
	var current any = payload
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}
