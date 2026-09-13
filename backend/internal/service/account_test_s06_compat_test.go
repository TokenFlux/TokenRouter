// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	gin "github.com/gin-gonic/gin"
	http "net/http"
)

// 测试适配只保留历史 Gin 白盒入口，生产平台执行直接使用事件运行上下文。
func accountTestRunFromGin(c *gin.Context) *accountTestRun {
	if value, ok := c.Get("s06_test_run"); ok {
		if run, ok := value.(*accountTestRun); ok {
			return run
		}
	}
	if c.Request.Header == nil {
		c.Request.Header = make(http.Header)
	}
	run := newAccountTestRun(c.Request.Context(), c.Request.Header, accounthttp.NewTestEventSink(c.Writer))
	run.get = c.Get
	run.set = c.Set
	c.Set("s06_test_run", run)
	return run
}
func (s *AccountTestService) TestAccountConnection(c *gin.Context, id int64, model, prompt, mode string, types ...string) error {
	request := accountcore.TestRequest{AccountID: id, Model: model, Prompt: prompt, Mode: mode, UserAgent: c.GetHeader("User-Agent"), Originator: c.GetHeader("originator")}
	if len(types) > 0 {
		v := types[0]
		request.Type = &v
	}
	if options, automatic := accountTestBackgroundOptionsFromContext(c.Request.Context()); automatic {
		request.Automatic = true
		request.UserAgent = options.userAgent
	}
	return s.Tester().Test(c.Request.Context(), request, accounthttp.NewTestEventSink(c.Writer))
}
func (s *AccountTestService) TestAccountConnectionWithType(c *gin.Context, id int64, model, prompt, kind, mode string, protocols ...string) error {
	request := accountcore.TestRequest{AccountID: id, Model: model, Prompt: prompt, Mode: mode, Type: &kind, UserAgent: c.GetHeader("User-Agent"), Originator: c.GetHeader("originator")}
	if len(protocols) > 0 {
		request.Protocol = protocols[0]
	}
	if options, automatic := accountTestBackgroundOptionsFromContext(c.Request.Context()); automatic {
		request.Automatic = true
		request.UserAgent = options.userAgent
	}
	return s.Tester().Test(c.Request.Context(), request, accounthttp.NewTestEventSink(c.Writer))
}

func (s *AccountTestService) testOpenAIAccountConnection(c *gin.Context, account *Account, modelID string, prompt string, mode string, testTypes ...string) error {
	run := accountTestRunFromGin(c)
	err := s.testOpenAIAccountConnectionRun(run, account, modelID, prompt, mode, testTypes...)
	return run.result(err)
}

func (s *AccountTestService) testOpenAIImageAPIKey(c *gin.Context, ctx context.Context, account *Account, modelID, prompt string) error {
	run := accountTestRunFromGin(c)
	err := s.testOpenAIImageAPIKeyRun(run, ctx, account, modelID, prompt)
	return run.result(err)
}

func (s *AccountTestService) testOpenAIImageOAuth(c *gin.Context, ctx context.Context, account *Account, modelID, prompt string) error {
	run := accountTestRunFromGin(c)
	err := s.testOpenAIImageOAuthRun(run, ctx, account, modelID, prompt)
	return run.result(err)
}
