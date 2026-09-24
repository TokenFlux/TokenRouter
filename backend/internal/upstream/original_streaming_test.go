//go:build unit

package upstream_test

import (
	"context"
	"errors"
	"io"
	"net"

	"syscall"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/stretchr/testify/require"
)

// --- parseSSEUsage 测试 ---

func TestSanitizeStreamError_StripsNetworkAddresses(t *testing.T) {
	src, err := net.ResolveTCPAddr("tcp", "10.0.0.1:54321")
	require.NoError(t, err)
	dst, err := net.ResolveTCPAddr("tcp", "52.1.2.3:443")
	require.NoError(t, err)

	raw := &net.OpError{
		Op:     "read",
		Net:    "tcp",
		Source: src,
		Addr:   dst,
		Err:    syscall.ECONNRESET,
	}

	// 前置：原始 Error() 确实包含会泄露的字段（避免测试在 Go 行为变化时静默通过）
	require.Contains(t, raw.Error(), "10.0.0.1")
	require.Contains(t, raw.Error(), "52.1.2.3")

	got := upstream.SanitizeStreamError(raw)
	require.NotContains(t, got, "10.0.0.1", "不得泄露内部源 IP")
	require.NotContains(t, got, "54321", "不得泄露源端口")
	require.NotContains(t, got, "52.1.2.3", "不得泄露上游目标 IP")
	require.NotContains(t, got, "443", "不得泄露上游端口")
	require.Equal(t, "connection reset by peer", got)
}

func TestSanitizeStreamError_KnownErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"unexpected EOF", io.ErrUnexpectedEOF, "unexpected EOF"},
		{"EOF", io.EOF, "EOF"},
		{"context canceled", context.Canceled, "canceled"},
		{"deadline exceeded", context.DeadlineExceeded, "deadline exceeded"},
		{"ECONNRESET 直接", syscall.ECONNRESET, "connection reset by peer"},
		{"EPIPE", syscall.EPIPE, "broken pipe"},
		{"ETIMEDOUT", syscall.ETIMEDOUT, "connection timed out"},
		{"未识别错误兜底", errors.New("weird internal error"), "upstream connection error"},
		{"nil 返回空串", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, upstream.SanitizeStreamError(tc.err))
		})
	}
}

// failover ResponseBody 必须用 sanitize 过的消息，避免泄露给客户端 / 写入 ops 日志
// 时携带内部地址信息。
