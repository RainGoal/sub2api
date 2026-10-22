package videoprovider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func parseZhiqiObject(body []byte) (map[string]any, error) {
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil || payload == nil || decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("zhiqi returned invalid JSON")
	}
	return payload, nil
}

func (zhiqiDriver) ParseTask(body []byte, fallbackTaskID string) (Task, error) {
	payload, err := parseZhiqiObject(body)
	if err != nil {
		return Task{}, err
	}
	task := Task{ID: firstPathText(payload, "id"), Model: firstPathText(payload, "model")}
	if task.ID == "" {
		task.ID = strings.TrimSpace(fallbackTaskID)
	}
	if task.ID == "" {
		return Task{}, fmt.Errorf("zhiqi returned no task id")
	}
	if _, err := escapedTaskID(task.ID); err != nil {
		return Task{}, err
	}
	task.Status = normalizeBBLabuStatus(firstPathText(payload, "status"))
	if task.Status == "expired" {
		task.Status = StatusFailed
	}
	// This protocol only documents usage.duration_seconds as output metering.
	// Request echoes, progress, and wall-clock elapsed time are not video usage.
	task.DurationSecondsExact, err = firstVideoExactSeconds(payload, "usage.duration_seconds")
	if err != nil {
		return Task{}, err
	}
	task.DurationSeconds = int(task.DurationSecondsExact)
	return task, nil
}

func (zhiqiDriver) ParseContentURL(body []byte) (string, error) {
	payload, err := parseZhiqiObject(body)
	if err != nil {
		return "", err
	}
	if payload["error"] != nil {
		return "", fmt.Errorf("zhiqi returned a content delivery error")
	}
	contentURL := firstPathText(payload, "url")
	if contentURL == "" {
		return "", fmt.Errorf("zhiqi returned no content URL")
	}
	if err := validatePublicHTTPSURL(contentURL); err != nil {
		return "", fmt.Errorf("zhiqi returned an invalid content URL: %w", err)
	}
	return contentURL, nil
}
