//go:build unit

// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	gin "github.com/gin-gonic/gin"
	io "io"
)

func (s *AccountTestService) testBedrockAccountConnection(c *gin.Context, ctx context.Context, account *Account, testModelID string, prompt string) error {
	run := accountTestRunFromGin(c)
	err := s.testBedrockAccountConnectionRun(run, ctx, account, testModelID, prompt)
	return run.result(err)
}

func (s *AccountTestService) testGrokAccountConnection(c *gin.Context, account *Account, modelID string, testArgs ...string) error {
	run := accountTestRunFromGin(c)
	err := s.testGrokAccountConnectionRun(run, account, modelID, testArgs...)
	return run.result(err)
}

func (s *AccountTestService) processGeminiStream(c *gin.Context, body io.Reader) error {
	run := accountTestRunFromGin(c)
	err := s.processGeminiStreamRun(run, body)
	return run.result(err)
}

func (s *AccountTestService) processOpenAIStream(c *gin.Context, body io.Reader) error {
	run := accountTestRunFromGin(c)
	err := s.processOpenAIStreamRun(run, body)
	return run.result(err)
}
