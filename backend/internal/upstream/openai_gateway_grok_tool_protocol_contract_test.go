//go:build unit

package upstream_test

import (
	"bufio"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/stretchr/testify/require"
)

func TestGrokResponsesClientToolStreamBodyFlushesFrameBeforeEOF(t *testing.T) {
	sourceReader, sourceWriter := io.Pipe()
	body := upstreamcore.NewResponsesClientToolStreamBody(sourceReader, bridge.ResponsesClientToolMapping{
		CustomTools: map[string]bool{"apply_patch": true},
	}, 500*1024*1024)
	defer func() { _ = body.Close() }()
	defer func() { _ = sourceWriter.Close() }()

	type readResult struct {
		frame string
		err   error
	}
	read := make(chan readResult, 1)
	go func() {
		reader := bufio.NewReader(body)
		var frame strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				read <- readResult{err: err}
				return
			}
			if _, err := frame.WriteString(line); err != nil {
				read <- readResult{err: err}
				return
			}
			if strings.TrimSpace(line) == "" {
				read <- readResult{frame: frame.String()}
				return
			}
		}
	}()

	firstFrame := "event: response.created\n" +
		`data: {"type":"response.created","sequence_number":0,"response":{"id":"flush-before-eof"}}` + "\n\n"
	_, err := sourceWriter.Write([]byte(firstFrame))
	require.NoError(t, err)

	select {
	case result := <-read:
		require.NoError(t, result.err)
		require.Contains(t, result.frame, "flush-before-eof")
		require.Contains(t, result.frame, "event: response.created")
	case <-time.After(3 * time.Second):
		t.Fatal("first transformed SSE frame was not flushed while the upstream connection remained open")
	}
}
