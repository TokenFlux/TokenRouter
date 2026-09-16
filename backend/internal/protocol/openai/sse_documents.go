package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

const (
	MaxConcatenatedJSONDocuments = 16
	MaxConcatenatedJSONBytes     = 16 * 1024 * 1024
)

// SplitConcatenatedJSONDocuments 仅识别多个完整 Responses 事件黏连在同一传输消息中的形态。
// 其他畸形载荷保持原样，继续交给既有错误路径处理。
func SplitConcatenatedJSONDocuments(payload []byte) ([][]byte, bool) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || len(payload) > MaxConcatenatedJSONBytes || json.Valid(payload) {
		return nil, false
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	documents := make([][]byte, 0, 2)
	for {
		var raw json.RawMessage
		err := decoder.Decode(&raw)
		if err != nil {
			if err == io.EOF && len(documents) > 1 {
				return documents, true
			}
			return nil, false
		}
		raw = bytes.TrimSpace(raw)
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, false
		}
		eventType := strings.TrimSpace(envelope.Type)
		if eventType == "" || strings.ContainsAny(eventType, "\r\n") {
			return nil, false
		}
		if len(documents) == MaxConcatenatedJSONDocuments {
			return nil, false
		}
		documents = append(documents, raw)
	}
}

type SSEJSONDocumentScanner struct {
	scanner *bufio.Scanner
	pending []string
	current string
}

func NewSSEJSONDocumentScanner(scanner *bufio.Scanner) *SSEJSONDocumentScanner {
	return &SSEJSONDocumentScanner{scanner: scanner}
}

func (s *SSEJSONDocumentScanner) Scan() bool {
	if len(s.pending) > 0 {
		s.current = s.pending[0]
		s.pending = s.pending[1:]
		return true
	}
	if s.scanner == nil || !s.scanner.Scan() {
		return false
	}

	line := s.scanner.Text()
	data, ok := ExtractSSEDataLine(line)
	if !ok {
		s.current = line
		return true
	}
	if len(data) > MaxConcatenatedJSONBytes {
		s.current = line
		return true
	}
	documents, repaired := SplitConcatenatedJSONDocuments([]byte(data))
	if !repaired {
		s.current = line
		return true
	}

	expanded := make([]string, 0, len(documents)*3)
	for i, document := range documents {
		if i > 0 {
			var envelope struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(document, &envelope)
			expanded = append(expanded, "event: "+strings.TrimSpace(envelope.Type))
		}
		expanded = append(expanded, "data: "+string(document), "")
	}
	s.current = expanded[0]
	s.pending = expanded[1:]
	return true
}

func (s *SSEJSONDocumentScanner) Text() string {
	return s.current
}

func (s *SSEJSONDocumentScanner) Err() error {
	if s.scanner == nil {
		return nil
	}
	return s.scanner.Err()
}
