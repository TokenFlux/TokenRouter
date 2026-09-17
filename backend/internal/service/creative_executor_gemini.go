package service

import (
	"errors"
	"strings"
)

// validateGeminiBaseURL 校验 Gemini base URL；失败时直接返回错误，禁止改变数据发送目标。
func (s *OpenAIGatewayService) validateGeminiBaseURL(raw string) (string, error) {
	validated, err := s.validateUpstreamBaseURL(raw)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(validated) == "" {
		return "", errors.New("gemini base url is empty")
	}
	return validated, nil
}
