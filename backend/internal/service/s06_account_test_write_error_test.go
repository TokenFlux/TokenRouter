//go:build unit

package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type s06FailTestEventWriter struct {
	gin.ResponseWriter
	err error
}

func (w s06FailTestEventWriter) Write([]byte) (int, error) { return 0, w.err }

// 流事件无法写出时，测试不能把已经丢失的输出报告为执行成功。
func TestS06AccountTestPropagatesEventWriteFailure(t *testing.T) {
	ctx, _ := newTestContext()
	failed := errors.New("forced test event write failure")
	ctx.Writer = s06FailTestEventWriter{ResponseWriter: ctx.Writer, err: failed}
	svc := &AccountTestService{}
	err := svc.processOpenAIStream(ctx, strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"fixture\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"))
	require.ErrorIs(t, err, failed)
}
