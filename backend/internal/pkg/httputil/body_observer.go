package httputil

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

// ErrBodyObserverRejected means an ingress policy already wrote the rejection
// response. Readers must return without replacing it with a parse/read error.
var ErrBodyObserverRejected = errors.New("request body rejected by ingress policy")

type requestBodyObserverKey struct{}

type requestBodyObserver struct {
	once sync.Once
	fn   func([]byte) error
	err  error
}

// WithRequestBodyObserver observes the first decoded body before normalization,
// model mapping or protocol conversion. The callback must not retain or mutate
// the byte slice. Refilled PrereadBody instances do not trigger it again.
func WithRequestBodyObserver(ctx context.Context, observer func([]byte) error) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, requestBodyObserverKey{}, &requestBodyObserver{fn: observer})
}

func observeRequestBody(req *http.Request, body []byte) error {
	observer, _ := req.Context().Value(requestBodyObserverKey{}).(*requestBodyObserver)
	if observer == nil {
		return nil
	}
	observer.once.Do(func() { observer.err = observer.fn(body) })
	return observer.err
}
