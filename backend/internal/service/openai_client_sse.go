package service

import (
	"bufio"
	"bytes"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func rewriteOpenAIClientSSELine(line, publicModel string) (string, error) {
	data, isData := extractOpenAISSEDataLine(line)
	if !isData {
		// Some compatible upstreams return a JSON body to a streaming request.
		if !strings.HasPrefix(strings.TrimSpace(line), "{") {
			return line, nil
		}
		data = line
	}
	updated, err := rewriteOpenAIClientPayload([]byte(data), publicModel)
	if err != nil {
		return "", err
	}
	if bytes.Equal(updated, []byte(data)) {
		return line, nil
	}
	if isData {
		return "data: " + string(updated), nil
	}
	return string(updated), nil
}

func rewriteOpenAIClientSSEBody(body, publicModel string, maxEventBytes int) ([]byte, error) {
	if maxEventBytes <= 0 {
		maxEventBytes = defaultMaxLineSize
	}
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), maxEventBytes)
	events := newOpenAIClientSSEScanner(scanner, maxEventBytes)
	var out strings.Builder
	for events.Scan() {
		line, err := rewriteOpenAIClientSSELine(events.Text(), publicModel)
		if err != nil {
			return nil, err
		}
		_, _ = out.WriteString(line)
		_ = out.WriteByte('\n')
	}
	if err := events.Err(); err != nil {
		return nil, errOpenAIClientPayload
	}
	return []byte(out.String()), nil
}

// Non-streaming SSE responses must pass the same framing and JSON checks
// before raw terminal diagnostics can cause retries or account penalties.
func (s *OpenAIGatewayService) validateOpenAIClientSSEBody(body string) error {
	maxEventBytes := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxEventBytes = s.cfg.Gateway.MaxLineSize
	}
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), maxEventBytes)
	events := newOpenAIClientSSEScanner(scanner, maxEventBytes)
	for events.Scan() {
		data, isData := extractOpenAISSEDataLine(events.Text())
		if !isData {
			if !strings.HasPrefix(strings.TrimSpace(events.Text()), "{") {
				continue
			}
			data = events.Text()
		}
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			continue
		}
		if !gjson.Valid(data) || !gjson.Parse(data).IsObject() {
			return errOpenAIClientPayload
		}
	}
	if events.Err() != nil {
		return errOpenAIClientPayload
	}
	return nil
}

func buildOpenAIClientResponseFailedSSE(responseID, publicModel string, source []byte, fallback string) string {
	body := buildOpenAIResponseFailedSSE(responseID, publicModel, source, fallback)
	// The legacy builder intentionally copies only code/type/message. Keep all
	// verified recovery metadata when adapting a bare upstream error to a
	// response.failed event, including top-level error-event codes.
	if len(bytes.TrimSpace(source)) > 0 {
		rawError := source
		for _, path := range []string{"response.error", "error"} {
			if value := gjson.GetBytes(source, path); value.Exists() && value.Type != gjson.Null {
				rawError = []byte(value.Raw)
				break
			}
		}
		if _, payload, ok := extractOpenAISSETerminalEvent(body); ok {
			cleanError := sanitizeOpenAIClientErrorObject(rawError, http.StatusBadGateway)
			if updated, err := sjson.SetRawBytes(payload, "response.error", cleanError); err == nil {
				body = "event: response.failed\ndata: " + string(updated) + "\n\n"
			}
		}
	}
	if _, payload, ok := extractOpenAISSETerminalEvent(body); ok {
		if updated, changed := sanitizeOpenAICapacityShedErrorCodeForClient(payload); changed {
			body = "event: response.failed\ndata: " + string(updated) + "\n\n"
		}
	}
	if updated, err := rewriteOpenAIClientSSEBody(body, publicModel, defaultMaxLineSize); err == nil {
		return string(updated)
	}
	return buildOpenAIResponseFailedSSE(responseID, "", nil, "The upstream response could not be processed. Please try again.")
}
