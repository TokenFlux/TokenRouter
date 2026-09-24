package openaiforward

import (
	"bufio"
	"io"
	"strings"
	"testing"
	"time"
)

func TestAnthropicNativeLinePump_TimesOutWithoutData(t *testing.T) {
	pr, _ := io.Pipe()
	scanner := bufio.NewScanner(pr)
	defer func() { _ = pr.Close() }()

	pump := NewAnthropicLinePump(scanner, 50*time.Millisecond)
	defer pump.Stop()

	start := time.Now()
	_, err := pump.Next()
	if err == nil || !strings.Contains(err.Error(), "stream data interval timeout") {
		t.Fatalf("expected interval timeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout not respected: %v", elapsed)
	}
}

func TestAnthropicNativeLinePump_DataResetsTimer(t *testing.T) {
	pr, pw := io.Pipe()
	scanner := bufio.NewScanner(pr)
	pump := NewAnthropicLinePump(scanner, 1*time.Second)
	defer pump.Stop()

	go func() {
		_, _ = pw.Write([]byte("event: ping\n"))
		// 保持流打开且不再发数据：第二次 next 必须超时。
		time.Sleep(3 * time.Second)
		_ = pw.Close()
	}()
	defer func() { _ = pr.Close() }()

	line, err := pump.Next()
	if err != nil || line != "event: ping" {
		t.Fatalf("expected first line, got %q err=%v", line, err)
	}

	start := time.Now()
	_, err = pump.Next()
	if err == nil || !strings.Contains(err.Error(), "stream data interval timeout") {
		t.Fatalf("expected interval timeout after data stops, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout not respected: %v", elapsed)
	}
}
