package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	maxOpenAIConcatenatedJSONDocuments = 16
	maxOpenAIConcatenatedJSONBytes     = 16 * 1024 * 1024
)

// splitOpenAIConcatenatedJSONDocuments recognizes the narrow corruption shape
// produced when multiple complete Responses events arrive in one transport
// message. Other malformed payloads are left untouched for normal error paths.
func splitOpenAIConcatenatedJSONDocuments(payload []byte) ([][]byte, bool) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || len(payload) > maxOpenAIConcatenatedJSONBytes || json.Valid(payload) {
		return nil, false
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	documents := make([][]byte, 0, 2)
	for {
		var raw json.RawMessage
		err := decoder.Decode(&raw)
		if err != nil {
			if err == io.EOF && len(documents) > 1 {
				return documents, true
			}
			return nil, false
		}
		raw = bytes.TrimSpace(raw)
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, false
		}
		eventType := strings.TrimSpace(envelope.Type)
		if eventType == "" || strings.ContainsAny(eventType, "\r\n") {
			return nil, false
		}
		if len(documents) == maxOpenAIConcatenatedJSONDocuments {
			return nil, false
		}
		documents = append(documents, raw)
	}
}

type openAISSEJSONDocumentScanner struct {
	scanner       *bufio.Scanner
	pending       []string
	current       string
	maxEventBytes int
	err           error
	lookahead     []string
	onReadLine    func()
}

func newOpenAISSEJSONDocumentScanner(scanner *bufio.Scanner) *openAISSEJSONDocumentScanner {
	return &openAISSEJSONDocumentScanner{scanner: scanner}
}

// Presentation operates on a complete SSE event, never on a JSON fragment.
// The original scanner remains available to providers outside this policy.
func newOpenAIClientSSEScanner(scanner *bufio.Scanner, maxEventBytes int) *openAISSEJSONDocumentScanner {
	if maxEventBytes <= 0 {
		maxEventBytes = defaultMaxLineSize
	}
	return &openAISSEJSONDocumentScanner{scanner: scanner, maxEventBytes: maxEventBytes}
}

func (s *openAISSEJSONDocumentScanner) Scan() bool {
	if len(s.pending) > 0 {
		s.current = s.pending[0]
		s.pending = s.pending[1:]
		return true
	}
	if s.maxEventBytes > 0 {
		return s.scanClientEvent()
	}
	if s.scanner == nil || !s.scanner.Scan() {
		return false
	}

	line := s.scanner.Text()
	data, ok := extractOpenAISSEDataLine(line)
	if !ok {
		s.current = line
		return true
	}
	if len(data) > maxOpenAIConcatenatedJSONBytes {
		s.current = line
		return true
	}
	documents, repaired := splitOpenAIConcatenatedJSONDocuments([]byte(data))
	if !repaired {
		s.current = line
		return true
	}

	expanded := make([]string, 0, len(documents)*3)
	for i, document := range documents {
		if i > 0 {
			var envelope struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(document, &envelope)
			expanded = append(expanded, "event: "+strings.TrimSpace(envelope.Type))
		}
		expanded = append(expanded, "data: "+string(document), "")
	}
	s.current = expanded[0]
	s.pending = expanded[1:]
	return true
}

func (s *openAISSEJSONDocumentScanner) Text() string {
	return s.current
}

func (s *openAISSEJSONDocumentScanner) scanClientEvent() bool {
	if s.scanner == nil || s.err != nil {
		return false
	}
	var lines, data []string
	size := 0
	rawJSON := false
	lastDataLine := -1
	dataMayBeComplete := false
	for {
		line := ""
		if len(s.lookahead) > 0 {
			line = s.lookahead[0]
			s.lookahead = s.lookahead[1:]
		} else if s.scanner.Scan() {
			line = s.scanner.Text()
			if s.onReadLine != nil {
				s.onReadLine()
			}
		} else {
			break
		}
		// A comment is already a complete keepalive, even without a blank line.
		if len(lines) == 0 && strings.HasPrefix(line, ":") {
			s.current = line
			return true
		}
		if len(lines) == 0 {
			trimmed := strings.TrimSpace(line)
			rawJSON = strings.HasPrefix(trimmed, "{") || (strings.HasPrefix(trimmed, "[") && trimmed != "[DONE]")
		}
		if value, isData := extractOpenAISSEDataLine(line); isData && dataMayBeComplete && strings.TrimSpace(value) != "" {
			previous := strings.TrimSpace(strings.Join(data, "\n"))
			if previous == "[DONE]" || json.Valid([]byte(previous)) {
				// Preserve the existing handling of complete JSON data lines
				// without a blank separator. A synthetic blank would commit
				// output early and change staging limits and keepalive behavior.
				// Leave the following event/id/retry headers with the next
				// document; valid data-before-event ordering remains untouched.
				next := append([]string(nil), lines[lastDataLine+1:]...)
				next = append(next, line)
				s.lookahead = append(next, s.lookahead...)
				lines = lines[:lastDataLine+1]
				break
			}
		}
		size += len(line) + 1
		if size > s.maxEventBytes {
			s.err = fmt.Errorf("%w: SSE event exceeds size limit", errOpenAIClientPayload)
			return false
		}
		lines = append(lines, line)
		if rawJSON {
			// Compatible providers may answer stream=true with indented JSON.
			// Buffer only this bounded document, including internal blank lines.
			trimmed := strings.TrimSpace(line)
			if (strings.HasSuffix(trimmed, "}") || strings.HasSuffix(trimmed, "]")) && json.Valid([]byte(strings.Join(lines, "\n"))) {
				break
			}
			continue
		}
		if value, ok := extractOpenAISSEDataLine(line); ok {
			data = append(data, value)
			lastDataLine = len(lines) - 1
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				// Avoid repeatedly joining a growing pretty-printed document
				// while its final object/array delimiter has not arrived.
				dataMayBeComplete = strings.HasSuffix(trimmed, "}") || strings.HasSuffix(trimmed, "]")
			}
		}
		if line == "" {
			break
		}
	}
	if len(lines) == 0 {
		return false
	}
	if rawJSON {
		s.current = strings.Join(lines, "\n")
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(s.current)); err == nil {
			s.current = compact.String()
		}
		return true
	}
	joined := strings.Join(data, "\n")
	if len(data) > 1 && json.Valid([]byte(joined)) {
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(joined)); err == nil {
			joined = compact.String()
		}
	}
	wroteData := false
	for _, line := range lines {
		if _, ok := extractOpenAISSEDataLine(line); !ok {
			s.pending = append(s.pending, line)
			continue
		}
		if wroteData {
			continue
		}
		wroteData = true
		if documents, repaired := splitOpenAIConcatenatedJSONDocuments([]byte(joined)); repaired {
			for i, document := range documents {
				if i > 0 {
					var envelope struct {
						Type string `json:"type"`
					}
					_ = json.Unmarshal(document, &envelope)
					s.pending = append(s.pending, "", "event: "+strings.TrimSpace(envelope.Type))
				}
				s.pending = append(s.pending, "data: "+string(document))
			}
		} else if len(data) == 1 {
			s.pending = append(s.pending, line)
		} else {
			s.pending = append(s.pending, "data: "+joined)
		}
	}
	s.current = s.pending[0]
	s.pending = s.pending[1:]
	return true
}

func (s *openAISSEJSONDocumentScanner) Err() error {
	if s.err != nil {
		return s.err
	}
	if s.scanner == nil {
		return nil
	}
	err := s.scanner.Err()
	if s.maxEventBytes > 0 && err == bufio.ErrTooLong {
		return fmt.Errorf("%w: SSE line exceeds size limit", errOpenAIClientPayload)
	}
	return err
}
