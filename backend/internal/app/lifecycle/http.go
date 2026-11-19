package lifecycle

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// http.go runs the HTTP listener and coordinates server shutdown when
// application cancellation arrives. HTTP listening can end through a Serve
// error or through application cancellation. The listener is acquired before
// the serving goroutine starts, so address conflicts return directly. A
// buffered result channel lets the serving goroutine publish its final error
// while the caller is shutting down the server after cancellation.
//
// Cancellation starts a fresh five-second shutdown budget independent of the
// cancelled context. A failed graceful shutdown invokes Close, then the caller
// waits for Serve to return before combining errors. ErrServerClosed is the
// expected HTTP termination result. Background service draining belongs to the
// application after this HTTP phase, with its own budget.

// Serve listens at server.Addr and runs HTTP until serving ends or ctx is
// cancelled. Listener acquisition errors return before any serving goroutine
// starts, and ErrServerClosed is treated as expected termination.
//
// Cancellation invokes Shutdown with a separate five-second context, then
// falls back to Close if graceful shutdown fails. It waits for the serving
// result and joins unexpected serve or close errors with shutdown failure.
// Successful cancellation-driven shutdown returns nil; callers perform their
// background drain after this function returns.
func Serve(ctx context.Context, server *http.Server) error {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", server.Addr)
	if err != nil {
		return err
	}
	slog.Info("Server started", "addr", server.Addr)
	defer slog.Info("Server exited")
	// Serving can finish while Shutdown is still running. Buffer its one result
	// so that completion does not require the shutdown caller to receive immediately.
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	select {
	case err = <-result:
		// Server.Serve reports ErrServerClosed after Shutdown or Close stops
		// listening. Treat that termination as success so application cleanup can proceed.
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		slog.Info("Shutting down server...")
		// The application context is already cancelled. A fresh budget lets active
		// HTTP requests finish before background dependencies begin their separate drain.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = server.Shutdown(shutdownCtx)
		if err != nil {
			// Graceful shutdown could not finish within its context. Close active
			// HTTP connections and retain any close failure alongside the shutdown error.
			err = errors.Join(err, server.Close())
		}
		// Await Serve after listener shutdown. Discard its expected ErrServerClosed
		// result while retaining independent Shutdown, Close, and unexpected serving failures.
		serveErr := <-result
		if !errors.Is(serveErr, http.ErrServerClosed) {
			err = errors.Join(err, serveErr)
		}
		return err
	}
}
