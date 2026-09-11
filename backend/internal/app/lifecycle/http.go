package lifecycle

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"time"
)

// Serve 为正式服务与 setup 提供同一条监听失败和信号关闭路径。
// HTTP 的五秒预算独立于后续后台 drain，不会把已取消的运行 context 传给 Shutdown。
func Serve(ctx context.Context, server *http.Server) error {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", server.Addr)
	if err != nil {
		return err
	}
	log.Printf("Server started on %s", server.Addr)
	defer log.Println("Server exited")
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	select {
	case err = <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Println("Shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = server.Shutdown(shutdownCtx)
		if err != nil {
			err = errors.Join(err, server.Close())
		}
		serveErr := <-result
		if !errors.Is(serveErr, http.ErrServerClosed) {
			err = errors.Join(err, serveErr)
		}
		return err
	}
}
