package provider

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

var (
	canListenOnce sync.Once
	canListen     bool
	canListenErr  error
)

func localListenerAvailable() bool {
	canListenOnce.Do(func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			canListenErr = err
			canListen = false
			return
		}
		_ = ln.Close()
		canListen = true
	})
	return canListen
}

func newLocalTestServer(tb testing.TB, handler http.Handler) *httptest.Server {
	tb.Helper()
	if !localListenerAvailable() {
		tb.Skipf("local listeners are not permitted in this environment: %v", canListenErr)
	}
	return httptest.NewServer(handler)
}
