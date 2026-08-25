package videoprovider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func newBearerRequest(ctx context.Context, method, targetURL, apiKey, accept string, body []byte) (*http.Request, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("video provider API key is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if strings.TrimSpace(accept) != "" {
		req.Header.Set("Accept", accept)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}
