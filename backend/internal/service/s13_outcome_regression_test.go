//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestS13CreativeOutputFailureMustNotInferAgain(t *testing.T) {
	f := newCreativeWorkerFixture()
	id := "crun_s13_output_failure"
	seedCreativeRun(f, id, true)
	f.store.saveOutputErr = errors.New("redis unavailable")
	f.exec.result = &CreativeExecuteResult{Outputs: []CreativeOutput{{Index: 0, Bytes: []byte("img"), Mime: "image/png"}}, AccountID: 55}
	_, err := f.worker.process(context.Background(), id)
	require.NoError(t, err)
	f.store.saveOutputErr = nil
	_, err = f.worker.process(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, 1, f.exec.calls, "provider 已成功，保存输出失败后的恢复不能再次推理")
}
