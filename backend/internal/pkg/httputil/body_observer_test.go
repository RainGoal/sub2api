package httputil

import (
	"bytes"
	"compress/gzip"
	"errors"
	"testing"
)

func TestRequestBodyObserverSeesFirstDecodedBodyOnce(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(samplePayload)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := newRequestWithBody(t, compressed.Bytes(), "gzip")
	called := 0
	req = req.WithContext(WithRequestBodyObserver(req.Context(), func(body []byte) error {
		called++
		if string(body) != samplePayload {
			t.Fatalf("observer received %q", body)
		}
		if req.Header.Get("Content-Encoding") != "gzip" {
			t.Fatal("observer ran after request headers were rewritten")
		}
		return nil
	}))
	got, err := ReadRequestBodyWithPrealloc(req)
	if err != nil || string(got) != samplePayload {
		t.Fatalf("first read = %q, %v", got, err)
	}
	req.Body = NewPrereadBody(got)
	got, err = ReadRequestBodyWithPrealloc(req)
	if err != nil || string(got) != samplePayload {
		t.Fatalf("second read = %q, %v", got, err)
	}
	if called != 1 {
		t.Fatalf("observer called %d times, want 1", called)
	}
}

func TestRequestBodyObserverRejectionPropagatesWithoutMutatingRequest(t *testing.T) {
	req := newRequestWithBody(t, []byte(samplePayload), "")
	req = req.WithContext(WithRequestBodyObserver(req.Context(), func([]byte) error {
		return ErrBodyObserverRejected
	}))
	_, err := ReadRequestBodyWithPrealloc(req)
	if !errors.Is(err, ErrBodyObserverRejected) {
		t.Fatalf("expected rejection sentinel, got %v", err)
	}
	if req.ContentLength != int64(len(samplePayload)) {
		t.Fatalf("request content length changed: %d", req.ContentLength)
	}
}
