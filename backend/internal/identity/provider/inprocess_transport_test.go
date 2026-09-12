package provider

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// newInProcessTransport adapts an http.HandlerFunc into an http.RoundTripper without opening sockets.
// It captures the request body (if any) and then rewinds it before invoking the handler.
func newInProcessTransport(handler http.HandlerFunc, capture func(r *http.Request, body []byte)) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		if capture != nil {
			capture(r, body)
		}

		rec := httptest.NewRecorder()
		handler(rec, r)
		return rec.Result(), nil
	})
}
