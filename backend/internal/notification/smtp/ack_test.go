package smtp

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 在 QUIT 时取消可证明 DATA 的成功响应已被客户端处理；无 DATA 响应则保持结果不明。
func TestSMTPAcknowledgementCancellationBoundary(t *testing.T) {
	for _, ack := range []bool{true, false} {
		name := "unknown-without-ack"
		if ack {
			name = "cancel-after-ack"
		}
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer func() { _ = listener.Close() }()
			address, ok := listener.Addr().(*net.TCPAddr)
			require.True(t, ok)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var messages atomic.Int64
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
				write := func(value string) bool {
					if _, err := rw.WriteString(value + "\r\n"); err != nil {
						return false
					}
					return rw.Flush() == nil
				}
				if !write("220 localhost ESMTP") {
					return
				}
				for {
					line, err := rw.ReadString('\n')
					if err != nil {
						return
					}
					command := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(command, "EHLO"):
						if !write("250-localhost\r\n250 AUTH PLAIN") {
							return
						}
					case strings.HasPrefix(command, "AUTH"):
						if !write("235 authenticated") {
							return
						}
					case strings.HasPrefix(command, "MAIL"), strings.HasPrefix(command, "RCPT"):
						if !write("250 OK") {
							return
						}
					case command == "DATA":
						if !write("354 send data") {
							return
						}
						for {
							line, err = rw.ReadString('\n')
							if err != nil {
								return
							}
							if strings.TrimSpace(line) == "." {
								break
							}
						}
						messages.Add(1)
						if !ack {
							return
						}
						if !write("250 accepted") {
							return
						}
					case command == "QUIT":
						cancel()
						_ = write("500 nonstandard quit")
						return
					}
				}
			}()
			err = New().Send(ctx, &SMTPConfig{Host: "127.0.0.1", Port: address.Port, Username: "fixture", Password: "fixture", From: "sender@example.com"}, "recipient@example.com", "fixture", "fixture")
			<-finished
			require.Equal(t, int64(1), messages.Load())
			if ack {
				require.NoError(t, err)
				require.ErrorIs(t, ctx.Err(), context.Canceled)
			} else {
				require.Error(t, err)
			}
		})
	}
}
