package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

const (
	openAICollaborationPreludeBytes = 256 * 1024
	openAICollaborationPreludeWait  = 2 * time.Second
)

// Probe before downstream headers/events are committed. Only an empty
// lifecycle or verified blank text placeholder can be retried. The deadline releases buffered bytes even
// when the upstream stalls in the middle of an event.
func probeOpenAICollaborationResponse(ctx context.Context, resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	sse := isEventStreamResponse(resp.Header)
	if !sse && resp.StatusCode < 400 && !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	source := resp.Body
	reader, writer := io.Pipe()
	resp.Body = &responsesClientToolStreamBody{PipeReader: reader, source: source}
	decision := make(chan []byte, 1)
	release := make(chan struct{})
	go pumpCollaborationPrelude(source, writer, resp.StatusCode, sse, decision, release)
	timer := time.NewTimer(openAICollaborationPreludeWait)
	defer timer.Stop()
	select {
	case failure := <-decision:
		return failure
	case <-timer.C:
		close(release)
		return nil
	case <-ctx.Done():
		close(release)
		_ = resp.Body.Close()
		return nil
	}
}

type collaborationReadChunk struct {
	data []byte
	err  error
}

type collaborationChunkReader struct {
	chunks  <-chan collaborationReadChunk
	current collaborationReadChunk
}

func (r *collaborationChunkReader) Read(p []byte) (int, error) {
	for len(r.current.data) == 0 && r.current.err == nil {
		chunk, ok := <-r.chunks
		if !ok {
			return 0, io.EOF
		}
		r.current = chunk
	}
	n := copy(p, r.current.data)
	r.current.data = r.current.data[n:]
	if len(r.current.data) == 0 {
		return n, r.current.err
	}
	return n, nil
}

func pumpCollaborationPrelude(source io.ReadCloser, out *io.PipeWriter, status int, sse bool, decision chan<- []byte, release <-chan struct{}) {
	defer func() { _ = source.Close() }()
	defer func() { _ = out.Close() }()
	stop := make(chan struct{})
	defer close(stop)
	chunks := make(chan collaborationReadChunk, 1)
	// One in-progress read and one queued chunk: no unbounded read-ahead.
	go func() {
		defer close(chunks)
		for {
			buf := make([]byte, 16*1024)
			n, err := source.Read(buf)
			select {
			case chunks <- collaborationReadChunk{data: buf[:n], err: err}:
			case <-stop:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	var prefix bytes.Buffer
	var pending, bareError []byte
	var placeholder collaborationPlaceholder
	finish := func(failure []byte, readErr error) {
		select {
		case <-release:
			failure = nil
		default:
		}
		decision <- failure
		if _, err := out.Write(prefix.Bytes()); err != nil {
			return
		}
		if readErr == nil {
			_, readErr = io.Copy(out, &collaborationChunkReader{chunks: chunks})
		}
		if readErr != nil && readErr != io.EOF {
			_ = out.CloseWithError(readErr)
		}
	}
	for {
		select {
		case <-release:
			finish(nil, nil)
			return
		case chunk, ok := <-chunks:
			if !ok {
				finish(nil, io.EOF)
				return
			}
			_, _ = prefix.Write(chunk.data)
			if prefix.Len() >= openAICollaborationPreludeBytes {
				finish(nil, chunk.err)
				return
			}
			if !sse {
				if chunk.err != nil {
					var failure []byte
					data := prefix.Bytes()
					if chunk.err == io.EOF && json.Valid(data) && !collaborationPayloadHasOutput(data) && isOpenAICollaborationEncryptedContentError(status, data) {
						failure = data
					}
					finish(failure, chunk.err)
					return
				}
				continue
			}
			pending = append(pending, chunk.data...)
			for {
				end := collaborationSSEFrameEnd(pending)
				if end == 0 {
					break
				}
				kind, payload := collaborationSSEEvent(pending[:end])
				pending = pending[end:]
				if len(payload) == 0 {
					continue
				}
				switch kind {
				case "response.created", "response.in_progress":
					if collaborationPayloadHasOutput(payload) || !placeholder.responseMatches(payload) {
						finish(nil, chunk.err)
						return
					}
				case "response.output_item.added", "response.output_item.done",
					"response.content_part.added", "response.content_part.done",
					"response.output_text.delta", "response.output_text.done":
					if !placeholder.accept(kind, payload) {
						finish(nil, chunk.err)
						return
					}
				case "error":
					if placeholder.hasOutput(payload) || !placeholder.responseMatches(payload) || !isOpenAICollaborationEncryptedContentError(status, payload) {
						finish(nil, chunk.err)
						return
					}
					bareError = append([]byte(nil), payload...)
				case "response.failed":
					if isOpenAICollaborationEncryptedContentError(status, payload) && !placeholder.hasOutput(payload) && placeholder.responseMatches(payload) {
						finish(payload, chunk.err)
					} else {
						finish(nil, chunk.err)
					}
					return
				case "[DONE]":
					finish(bareError, chunk.err)
					return
				default:
					// completed overrides bare error; any text/tool/unknown event
					// closes the recovery window before it is sent downstream.
					finish(nil, chunk.err)
					return
				}
			}
			if chunk.err != nil {
				if chunk.err == io.EOF && len(pending) == 0 {
					finish(bareError, chunk.err)
				} else {
					finish(nil, chunk.err)
				}
				return
			}
		}
	}
}

func collaborationPayloadHasOutput(payload []byte) bool {
	for _, path := range []string{"output", "response.output"} {
		output := gjson.GetBytes(payload, path)
		if output.Exists() && output.Raw != "null" && (!output.IsArray() || len(output.Array()) > 0) {
			return true
		}
	}
	return false
}

func collaborationSSEFrameEnd(raw []byte) int {
	start := 0
	for i, b := range raw {
		if b != '\n' {
			continue
		}
		if len(bytes.TrimSuffix(raw[start:i], []byte{'\r'})) == 0 {
			return i + 1
		}
		start = i + 1
	}
	return 0
}

func collaborationSSEEvent(raw []byte) (string, []byte) {
	var event string
	var data [][]byte
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if bytes.HasPrefix(line, []byte("event:")) {
			event = strings.TrimSpace(string(line[len("event:"):]))
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			data = append(data, bytes.TrimPrefix(line[len("data:"):], []byte{' '}))
		}
	}
	payload := bytes.Join(data, []byte{'\n'})
	if bytes.Equal(payload, []byte("[DONE]")) {
		return "[DONE]", payload
	}
	if !json.Valid(payload) {
		return "", payload
	}
	if kind := gjson.GetBytes(payload, "type").String(); kind != "" {
		event = kind
	} else if event != "" {
		var document map[string]json.RawMessage
		if json.Unmarshal(payload, &document) == nil {
			document["type"], _ = json.Marshal(event)
			payload, _ = json.Marshal(document)
		}
	}
	return event, payload
}
