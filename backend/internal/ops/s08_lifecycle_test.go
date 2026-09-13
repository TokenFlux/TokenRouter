package ops

import (
	"context"
	"testing"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logevent"
)

func TestS08RegressionSystemLogDuplicateStart(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	r := &opsRepoMock{BatchInsertSystemLogsFn: func(_ context.Context, in []*OpsInsertSystemLogInput) (int64, error) {
		entered <- struct{}{}
		<-release
		return int64(len(in)), nil
	}}
	s := NewOpsSystemLogSink(r)
	s.Start()
	s.Start()
	for i := 0; i < 400; i++ {
		s.WriteLogEvent(&logger.LogEvent{Level: "error", Message: "planning"})
	}
	<-entered
	select {
	case <-entered:
		t.Error("duplicate Start created two concurrent system-log writers with separate backoff state")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	s.Stop()
}
