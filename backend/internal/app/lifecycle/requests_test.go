package lifecycle

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// handler 的尾部清理结束前，生命周期不能关闭其依赖。
func TestRequestsWaitIncludesHandlerTail(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	address := make(chan string, 1)
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("started"))
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("缺少 Flush 能力")
			return
		}
		flusher.Flush()
		close(entered)
		<-release
	}), BaseContext: func(ln net.Listener) context.Context { address <- ln.Addr().String(); return context.Background() }}
	manager := New()
	TrackRequests(server, manager)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, server) }()
	response, err := http.Get("http://" + <-address)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	<-entered
	cancel()
	closed := make(chan error, 1)
	go func() { closed <- manager.Stop(context.Background()) }()
	select {
	case <-closed:
		t.Fatal("handler 尾部清理未完成")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-closed)
	require.NoError(t, <-served)
}

// 不包装 ResponseWriter，并在 net/http 不管理的 hijack 连接上触发原有断开路径。
func TestRequestsCloseHijackedConnectionsBeforeWaiting(t *testing.T) {
	address := make(chan string, 1)
	hijacked := make(chan struct{})
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("缺少 Hijack 能力")
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: example\r\n\r\n")
		_ = rw.Flush()
		close(hijacked)
		_, _ = io.Copy(io.Discard, conn)
	}), BaseContext: func(ln net.Listener) context.Context { address <- ln.Addr().String(); return context.Background() }}
	manager := New()
	TrackRequests(server, manager)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, server) }()
	conn, err := net.Dial("tcp", <-address)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: example\r\n\r\n")
	require.NoError(t, err)
	_, err = http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	<-hijacked
	cancel()
	require.NoError(t, <-served)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	require.NoError(t, manager.Stop(stopCtx))
}

func TestServeReturnsListenerFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	require.Error(t, Serve(context.Background(), &http.Server{Addr: ln.Addr().String()}))
}

// 升级晚于关闭快照时，ConnState 仍须关闭该连接，不能遗留到总预算超时。
func TestRequestsCloseLateHijack(t *testing.T) {
	manager := New()
	server := &http.Server{}
	TrackRequests(server, manager)
	require.NoError(t, manager.Stop(context.Background()))
	serverSide, clientSide := net.Pipe()
	defer func() { _ = serverSide.Close(); _ = clientSide.Close() }()
	require.NoError(t, clientSide.SetReadDeadline(time.Now().Add(time.Second)))
	server.ConnState(serverSide, http.StateHijacked)
	_, err := clientSide.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF)
}
