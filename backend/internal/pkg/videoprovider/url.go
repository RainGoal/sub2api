package videoprovider

import (
	"fmt"
	"net/url"
	"strings"
)

func buildProviderURL(baseURL, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid video provider base URL")
	}
	return strings.TrimRight(parsed.String(), "/") + path, nil
}

func escapedTaskID(taskID string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || taskID == "." || taskID == ".." || strings.ContainsAny(taskID, "\x00\r\n") {
		return "", fmt.Errorf("invalid video provider task id")
	}
	return url.PathEscape(taskID), nil
}
