package videoprovider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

func (videoTasksJSONDriver) ParseTask(body []byte, fallbackTaskID string) (Task, error) {
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil || payload == nil || decoder.Decode(new(any)) != io.EOF {
		return Task{}, fmt.Errorf("video provider returned invalid task JSON")
	}
	task := Task{ID: firstPathText(payload, "id", "task_id", "data.id", "data.task_id")}
	if task.ID == "" {
		task.ID = strings.TrimSpace(fallbackTaskID)
	}
	if task.ID == "" {
		return Task{}, fmt.Errorf("video provider returned no task id")
	}
	if _, err := escapedTaskID(task.ID); err != nil {
		return Task{}, err
	}
	task.Model = firstPathText(payload, "model", "data.model")
	task.Resolution = canonicalResolution(firstPathText(payload, "resolution", "data.resolution", "video.resolution", "properties.resolution"))
	rawStatus := firstPathText(payload, "status", "data.status")
	if strings.EqualFold(rawStatus, "failure") {
		rawStatus = "failed"
	}
	task.Status = normalizeBBLabuStatus(rawStatus)
	var err error
	task.DurationSecondsExact, err = firstVideoExactSeconds(payload, "duration", "data.duration", "video.duration", "properties.duration", "seconds")
	if err != nil {
		return Task{}, err
	}
	task.ReferenceInputSecondsExact, err = firstVideoExactSeconds(payload,
		"usage.reference_video_seconds", "usage.reference_video_input_seconds", "usage.reference_video_duration",
		"usage.input_video_seconds", "usage.input_video_duration", "billing.reference_video_seconds",
		"billing.reference_video_duration_seconds", "data.usage.reference_video_seconds",
		"data.usage.reference_video_input_seconds", "properties.reference_video_duration")
	if err != nil {
		return Task{}, err
	}
	// Keep old integer fields for callers compiled against the legacy drivers;
	// the new protocol's billing must use the exact fields above.
	task.DurationSeconds = int(task.DurationSecondsExact)
	task.ReferenceInputSeconds = int(task.ReferenceInputSecondsExact)
	return task, nil
}

func firstVideoExactSeconds(payload map[string]any, paths ...string) (float64, error) {
	for _, path := range paths {
		value := pathValue(payload, path)
		if value == nil {
			continue
		}
		number, ok := value.(json.Number)
		// OpenAI-compatible video responses can encode output seconds as a string.
		// Keep duration and reference metering fields strictly numeric.
		if encoded, isString := value.(string); !ok && isString && path == "seconds" {
			encoded = strings.TrimSpace(encoded)
			if json.Valid([]byte(encoded)) {
				number, ok = json.Number(encoded), true
			}
		}
		if !ok {
			return 0, fmt.Errorf("video duration %s must be a JSON number", path)
		}
		seconds, err := number.Float64()
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > 3600 {
			return 0, fmt.Errorf("video duration %s is outside the supported range", path)
		}
		return seconds, nil
	}
	return 0, nil
}
