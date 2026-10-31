package provider

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hashicorp/go-hclog"
)

// TestProcessLogsRetainBoundedOrderedCopies writes past the ring capacity.
// The retained slice must contain only the latest five hundred records, and
// mutating a returned snapshot must not change subsequent administrator reads.
func TestProcessLogsRetainBoundedOrderedCopies(t *testing.T) {
	now := time.Now()
	logs, _ := newProcessLogs(slog.New(slog.NewTextHandler(io.Discard, nil)), "test.plugin", "1.0.0", func() time.Time { return now })
	for i := range 600 {
		if i%200 == 0 {
			now = now.Add(time.Second)
		}
		logs.Accept("", hclog.Info, fmt.Sprint(i))
	}
	lines := logs.snapshot()
	if len(lines) != 500 || lines[0].Message != "100" || lines[499].Message != "599" {
		t.Fatal("ring ordering or capacity changed")
	}
	lines[0].Message = "changed"
	if logs.snapshot()[0].Message != "100" {
		t.Fatal("snapshot shares ring storage")
	}
}

// TestProcessLogsEnforceRollingSecondBudget fills the admitted burst and advances
// only half a second. No extra line may enter until the oldest admitted record
// leaves the full one-second window, independently of summary reporting time.
func TestProcessLogsEnforceRollingSecondBudget(t *testing.T) {
	now := time.Now()
	logs, _ := newProcessLogs(slog.New(slog.NewTextHandler(io.Discard, nil)), "test.plugin", "1.0.0", func() time.Time { return now })
	for range 200 {
		logs.Accept("", hclog.Info, "line")
	}
	now = now.Add(500 * time.Millisecond)
	logs.Accept("", hclog.Info, "suppressed")
	if len(logs.snapshot()) != 200 || logs.dropped != 1 {
		t.Fatal("rolling log limit admitted an early line")
	}
	now = now.Add(500 * time.Millisecond)
	logs.Accept("", hclog.Info, "accepted")
	if len(logs.snapshot()) != 201 {
		t.Fatal("expired admission did not free capacity")
	}
}

// TestProcessOutputJoinsFragmentsAndBoundsLongLines splits one message across
// writes and follows an oversized UTF-8 line with another message. Neither
// stream chunking nor truncation may corrupt the retained record boundaries.
func TestProcessOutputJoinsFragmentsAndBoundsLongLines(t *testing.T) {
	logs, _ := newProcessLogs(slog.New(slog.NewTextHandler(io.Discard, nil)), "test.plugin", "1.0.0", time.Now)
	output := &processOutput{logs: logs}
	_, _ = output.Write([]byte("hel"))
	_, _ = output.Write([]byte("lo\n"))
	_, _ = output.Write([]byte(strings.Repeat("字", 2000)))
	_, _ = output.Write([]byte("discarded\nnext"))
	output.flush()
	lines := logs.snapshot()
	if len(lines) != 3 || lines[0].Message != "hello" || lines[2].Message != "next" {
		t.Fatalf("incorrect line boundaries: %d", len(lines))
	}
	if len(lines[1].Message) > 4096 || !utf8.ValidString(lines[1].Message) {
		t.Fatal("line is oversized or malformed UTF-8")
	}
}
