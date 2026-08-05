package ollama

import (
	"context"
	"io"
	"net/http"
	"sync"
)

type ollamaResponseObserverContextKey struct{}

type ollamaResponseReadObserver struct {
	mu  sync.Mutex
	err error
}

func withOllamaResponseReadObserver(
	ctx context.Context,
) (context.Context, *ollamaResponseReadObserver) {
	observer := &ollamaResponseReadObserver{}
	return context.WithValue(ctx, ollamaResponseObserverContextKey{}, observer), observer
}

func (o *ollamaResponseReadObserver) Record(err error) {
	if err == nil || err == io.EOF {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err == nil {
		o.err = err
	}
}

func (o *ollamaResponseReadObserver) Err() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.err
}

type ollamaResponseObserverTransport struct {
	inner http.RoundTripper
}

func (t *ollamaResponseObserverTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.inner.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil {
		return response, err
	}
	observer, _ := request.Context().Value(ollamaResponseObserverContextKey{}).(*ollamaResponseReadObserver)
	if observer != nil {
		response.Body = &ollamaObservedResponseBody{ReadCloser: response.Body, observer: observer}
	}
	return response, nil
}

type ollamaObservedResponseBody struct {
	io.ReadCloser
	observer *ollamaResponseReadObserver
}

func (b *ollamaObservedResponseBody) Read(buffer []byte) (int, error) {
	count, err := b.ReadCloser.Read(buffer)
	b.observer.Record(err)
	return count, err
}
