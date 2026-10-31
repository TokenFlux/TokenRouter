package provider

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hashicorp/go-hclog"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// process_logs.go forwards bounded plugin diagnostics to the host logger and a
// recent-line buffer. One limiter covers library diagnostics, plugin structured
// stderr, and synchronized stdout, preventing those paths from multiplying the
// allowed log rate. The supervisor periodically flushes dropped-line summaries;
// this buffer does not create an independent background worker or shutdown owner.
//
// The ring retains at most five hundred copied records. Each accepted message
// is truncated on a UTF-8 boundary before publication, and reads return a new
// slice so administrator responses cannot modify retained history. Rate-limit
// counters and ring positions share the mutex while the downstream slog call
// occurs outside it, keeping a slow logger from blocking administrative reads.

// processLogs owns an installation's diagnostic admission and recent-line buffer.
type processLogs struct {
	logger *slog.Logger     // host logger with plugin identity
	now    func() time.Time // host observation clock
	// mu protects admitted timestamps, ring positions, dropped counts, and logger selection; downstream output runs after release so history reads remain available.
	mu            sync.Mutex
	admitted      []time.Time      // retained log admission timestamps
	nextAdmission int              // oldest admission replacement position
	lines         []plugin.LogLine // bounded retained diagnostic records
	next          int              // next ring replacement position
	dropped       int              // suppressed lines since report
	lastReport    time.Time        // previous drop summary timestamp
}

// processOutput assembles one standard stream into bounded logical log lines.
type processOutput struct {
	logs *processLogs // shared installation diagnostic sink
	// mu protects partial and discarding across stream chunks; a completed line reaches the shared sink before the next chunk can append.
	mu         sync.Mutex
	partial    []byte // bounded unfinished line bytes
	discarding bool   // oversized line remainder suppression
}

// newProcessLogs creates a bounded sink and go-plugin-compatible intercepted logger.
// The caller supplies a nonnil observation clock. No worker starts here;
// the supervisor owns periodic dropped-line summaries and final stream cleanup.
func newProcessLogs(logger *slog.Logger, id, version string, now func() time.Time) (*processLogs, hclog.Logger) {
	logs := &processLogs{logger: logger.With("component", "plugin", "plugin_id", id, "plugin_version", version), now: now, lastReport: now()}
	return logs, logs.bind(logger, id, version)
}

// bind assigns generation metadata while retaining the installation's recent lines.
// The process owner stops the previous generation before rebinding. Accepted
// records capture their logger under the mutex, so later metadata changes cannot race.
func (l *processLogs) bind(logger *slog.Logger, id, version string) hclog.Logger {
	l.mu.Lock()
	l.logger = logger.With("component", "plugin", "plugin_id", id, "plugin_version", version)
	l.mu.Unlock()
	interceptor := hclog.NewInterceptLogger(&hclog.LoggerOptions{Name: id, Level: hclog.Trace, Output: io.Discard})
	interceptor.RegisterSink(l)
	return interceptor
}

// Accept converts go-plugin diagnostics into one bounded host log record.
// Rate-limited records increase the dropped counter without entering the ring.
// Accepted records retain their level and complete UTF-8 prefix within four kibibytes.
//
// Ring publication and logger selection share the mutex, preserving the chosen
// generation metadata while a supervisor rebinds its logger. Downstream logging
// occurs after unlocking, so slow output cannot block a history snapshot. The
// circular position advances only when replacing a full buffer's oldest record.
func (l *processLogs) Accept(_ string, level hclog.Level, message string, args ...any) {
	l.mu.Lock()
	now := l.now()
	if len(l.admitted) == 200 && now.Sub(l.admitted[l.nextAdmission]) < time.Second {
		l.dropped++
		l.mu.Unlock()
		return
	}
	// Reuse only an admission older than the rolling one-second window, preserving burst diagnostics without exceeding the per-plugin line budget.
	if len(l.admitted) < 200 {
		l.admitted = append(l.admitted, now)
	} else {
		l.admitted[l.nextAdmission] = now
		l.nextAdmission = (l.nextAdmission + 1) % 200
	}
	l.mu.Unlock()
	if len(args) > 0 {
		var details strings.Builder
		for i := 0; i < len(args); i += 2 {
			if i+1 < len(args) {
				fmt.Fprintf(&details, " %v=%v", args[i], args[i+1])
			} else {
				fmt.Fprintf(&details, " %v", args[i])
			}
		}
		message += details.String()
	}
	message = strings.ToValidUTF8(message, "\uFFFD")
	if len(message) > 4096 {
		// Preserve a complete final rune when cutting a byte-limited line so JSON log consumers do not receive malformed UTF-8 text.
		message = message[:4096]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	severity := slog.LevelInfo
	switch level {
	case hclog.Trace, hclog.Debug:
		severity = slog.LevelDebug
	case hclog.Warn:
		severity = slog.LevelWarn
	case hclog.Error:
		severity = slog.LevelError
	}
	l.mu.Lock()
	line := plugin.LogLine{Time: l.now(), Level: severity.String(), Message: message}
	if len(l.lines) < 500 {
		l.lines = append(l.lines, line)
	} else {
		l.lines[l.next] = line
		l.next = (l.next + 1) % 500
	}
	logger := l.logger
	l.mu.Unlock()
	logger.Log(context.Background(), severity, message)
}

// snapshot returns retained diagnostics in publication order without sharing storage.
// An unfinished ring is copied directly. A full ring begins at the next
// replacement position, followed by the wrapped prefix containing newer records.
//
// The snapshot holds the ring mutex while copying both segments so a concurrent
// accepted line cannot move the split between copies. Callers receive their own
// slice and immutable message strings, allowing administrative serialization to
// proceed without holding the process logger's lock or changing retained history.
func (l *processLogs) snapshot() []plugin.LogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.lines) < 500 {
		return append([]plugin.LogLine(nil), l.lines...)
	}
	result := append([]plugin.LogLine(nil), l.lines[l.next:]...)
	return append(result, l.lines[:l.next]...)
}

// flushDrops emits at most one suppressed-line summary per minute.
// The supervisor invokes it during polling. A quiet interval with no discarded
// lines produces no additional host log entry.
func (l *processLogs) flushDrops() {
	l.mu.Lock()
	now := l.now()
	if now.Sub(l.lastReport) < time.Minute {
		l.mu.Unlock()
		return
	}
	dropped := l.dropped
	l.dropped = 0
	l.lastReport = now
	logger := l.logger
	l.mu.Unlock()
	if dropped > 0 {
		logger.Warn("Plugin log lines were rate limited", "dropped_lines", dropped)
	}
}

// Write assembles complete lines across stream chunks without unbounded buffering.
// A line exceeding four kibibytes emits its prefix once and discards its
// remainder until newline. Accepted byte counts include suppressed content.
//
// Fragment state belongs to one stream, so stdout and stderr cannot concatenate
// each other's partial messages. The stream mutex preserves chunk order while
// the shared sink applies installation-wide rate limiting and history retention.
// The owner flushes a final incomplete line only after the corresponding
// go-plugin stream has stopped writing during generation cleanup.
func (w *processOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, value := range data {
		if value == '\n' {
			if len(w.partial) > 0 {
				w.logs.Accept("", hclog.Info, string(w.partial))
			}
			w.partial = w.partial[:0]
			w.discarding = false
			continue
		}
		if w.discarding {
			continue
		}
		if len(w.partial) == 4096 {
			// Emit only the bounded prefix, then discard until newline so an unbroken stream cannot grow memory or create duplicate truncated records.
			w.logs.Accept("", hclog.Info, string(w.partial))
			w.partial = w.partial[:0]
			w.discarding = true
			continue
		}
		w.partial = append(w.partial, value)
	}
	return len(data), nil
}

// flush publishes a final incomplete line after its stream has stopped writing.
// Repeated calls with no remaining bytes do nothing. The owner must finish
// go-plugin stream cleanup before treating this flush as the final diagnostic boundary.
func (w *processOutput) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.partial) > 0 {
		w.logs.Accept("", hclog.Info, string(w.partial))
		w.partial = nil
	}
}
