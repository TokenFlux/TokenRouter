//go:build unit

package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/stretchr/testify/require"
)

type s06FailTestEventWriter struct {
	TestStreamWriter
	err error
}

func (w s06FailTestEventWriter) Write([]byte) (int, error) { return 0, w.err }

// 流事件无法写出时，测试不能把已经丢失的输出报告为执行成功。
func TestS06AccountTestPropagatesEventWriteFailure(t *testing.T) {
	failed := errors.New("forced test event write failure")
	writer := s06FailTestEventWriter{TestStreamWriter: httptest.NewRecorder(), err: failed}
	run := provider.NewTestRun(t.Context(), make(http.Header), NewTestEventSink(writer))
	defer run.Cancel()
	err := (provider.TestStreamOutput{}).Responses(run, strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"fixture\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"))
	err = run.Result(err)
	require.ErrorIs(t, err, failed)
}
