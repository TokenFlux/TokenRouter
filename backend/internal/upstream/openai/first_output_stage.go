// 首输出暂存、磁盘回退与 scanner 上限只持有当前尝试状态，不改变提交边界。
package openai

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync/atomic"
)

const (
	OpenAIFirstOutputStageMemoryLimit        = 64 * 1024
	OpenAIFirstOutputStageMaxBytes           = 8 * 1024 * 1024
	OpenAIFirstOutputScannerFramingAllowance = 64
	OpenAIFirstOutputGuardQueueSize          = 1
	OpenAIDefaultStreamQueueSize             = 16
)

var (
	ErrOpenAIFirstOutputStageLimit   = errors.New("openai first-output staging limit exceeded")
	ErrOpenAIFirstOutputScannerLimit = errors.New("openai pre-output scanner token limit exceeded")
)

type OpenAIFirstOutputStage struct {
	limit      int64
	size       int64
	memory     bytes.Buffer
	tempFile   *os.File
	tempPath   string
	createTemp func() (*os.File, error)
	removeFile func(string) error
	memoryOnly bool
	cleanupErr error
	closed     bool
}

func NewOpenAIFirstOutputStage(limit int64) *OpenAIFirstOutputStage {
	if limit < 1 {
		limit = 1
	}
	return &OpenAIFirstOutputStage{
		limit:      limit,
		createTemp: func() (*os.File, error) { return os.CreateTemp("", "tokenrouter-openai-first-output-*") },
		removeFile: os.Remove,
		memoryOnly: runtime.GOOS == "windows",
	}
}

func NewDefaultOpenAIFirstOutputStage() *OpenAIFirstOutputStage {
	return NewOpenAIFirstOutputStage(OpenAIFirstOutputStageMaxBytes)
}

func OpenAIFirstOutputEventQueueSize(guardFirstOutput bool) int {
	if guardFirstOutput {
		return OpenAIFirstOutputGuardQueueSize
	}
	return OpenAIDefaultStreamQueueSize
}

func OpenAIFirstOutputDynamicScanLines(guardActive *atomic.Bool) bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		advance, token, err = bufio.ScanLines(data, atEOF)
		if err != nil || guardActive == nil || !guardActive.Load() {
			return advance, token, err
		}
		limit := OpenAIFirstOutputStageMaxBytes + OpenAIFirstOutputScannerFramingAllowance
		if token != nil {
			if len(token) > limit {
				return 0, nil, ErrOpenAIFirstOutputScannerLimit
			}
			return advance, token, nil
		}
		// 已达到上限且没有分隔符时，再读取一个字节必然超过受保护 token 预算；
		// 在 Scanner 继续增长到 MaxLineSize 前失败。
		if len(data) >= limit {
			return 0, nil, ErrOpenAIFirstOutputScannerLimit
		}
		return advance, token, nil
	}
}

func (s *OpenAIFirstOutputStage) Buffered() int64 {
	if s == nil {
		return 0
	}
	return s.size
}

func (s *OpenAIFirstOutputStage) WriteString(value string) (int, error) {
	if err := s.prepareWrite(len(value)); err != nil {
		return 0, err
	}
	var n int
	var err error
	if s.tempFile == nil {
		n, err = s.memory.WriteString(value)
	} else {
		n, err = io.WriteString(s.tempFile, value)
	}
	s.size += int64(n)
	if err != nil {
		return n, fmt.Errorf("write first-output stage: %w", err)
	}
	return n, nil
}

func (s *OpenAIFirstOutputStage) Write(p []byte) (int, error) {
	if err := s.prepareWrite(len(p)); err != nil {
		return 0, err
	}
	var n int
	var err error
	if s.tempFile == nil {
		n, err = s.memory.Write(p)
	} else {
		n, err = s.tempFile.Write(p)
	}
	s.size += int64(n)
	if err != nil {
		return n, fmt.Errorf("write first-output stage: %w", err)
	}
	return n, nil
}

func (s *OpenAIFirstOutputStage) prepareWrite(incoming int) error {
	if s == nil || s.closed {
		return os.ErrClosed
	}
	if int64(incoming) > s.limit-s.size {
		return fmt.Errorf("%w: buffered=%d incoming=%d limit=%d", ErrOpenAIFirstOutputStageLimit, s.size, incoming, s.limit)
	}
	if s.tempFile != nil || s.memoryOnly || s.size+int64(incoming) <= OpenAIFirstOutputStageMemoryLimit {
		return nil
	}
	file, err := s.createTemp()
	if err != nil {
		return fmt.Errorf("create first-output spool: %w", err)
	}
	path := file.Name()
	// 写入任何请求数据前先 unlink。Unix 仍可通过文件描述符读取，进程崩溃或 SIGKILL
	// 也不会留下带名称的明文暂存文件。
	if unlinkErr := s.removeFile(path); unlinkErr != nil {
		closeErr := file.Close()
		removeErr := s.removeFile(path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		s.memoryOnly = true
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			s.tempPath = path
		}
		s.cleanupErr = errors.Join(
			s.cleanupErr,
			fmt.Errorf("unlink first-output spool before use: %w", unlinkErr),
			closeErr,
			removeErr,
		)
		return nil
	}
	if _, err := file.Write(s.memory.Bytes()); err != nil {
		_ = file.Close()
		return fmt.Errorf("initialize first-output spool: %w", err)
	}
	s.tempFile = file
	s.tempPath = path
	s.memory.Reset()
	return nil
}

func (s *OpenAIFirstOutputStage) CommitTo(dst io.Writer) error {
	if s == nil || s.closed {
		return os.ErrClosed
	}
	if s.tempFile == nil {
		if _, err := io.Copy(dst, bytes.NewReader(s.memory.Bytes())); err != nil {
			return err
		}
	} else {
		if _, err := s.tempFile.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("seek first-output spool: %w", err)
		}
		if _, err := io.CopyN(dst, s.tempFile, s.size); err != nil {
			return err
		}
	}
	if err := s.Close(); err != nil {
		// 数据已成功交付；保留清理错误供 handler 的延迟清理/日志阶段处理，
		// 不把已经提交的字节转换成流错误。
		s.cleanupErr = errors.Join(s.cleanupErr, err)
	}
	return nil
}

func (s *OpenAIFirstOutputStage) Close() error {
	if s == nil {
		return nil
	}
	if s.closed && s.tempFile == nil && s.tempPath == "" && s.cleanupErr == nil {
		return nil
	}
	s.closed = true
	s.size = 0
	s.memory.Reset()
	closeErr := s.cleanupErr
	s.cleanupErr = nil
	if s.tempFile != nil {
		closeErr = errors.Join(closeErr, s.tempFile.Close())
		s.tempFile = nil
	}
	if s.tempPath != "" {
		removeErr := s.removeFile(s.tempPath)
		if removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
			s.tempPath = ""
		} else {
			closeErr = errors.Join(closeErr, removeErr)
		}
	}
	return closeErr
}

// Closed 供兼容输出器判断提交后的写入路径，不允许外部修改暂存状态。
func (s *OpenAIFirstOutputStage) Closed() bool { return s.closed }

// Limit 只读返回当前尝试的暂存上限，供调用者核对其与 scanner 预算的区别。
func (s *OpenAIFirstOutputStage) Limit() int64 { return s.limit }
