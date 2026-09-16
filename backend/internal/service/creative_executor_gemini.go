package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	geminiwire "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"

	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

type creativeGeminiGenerateRequest = geminiwire.ImageGenerateRequest

// executeGemini 执行 Gemini 平台任务（含 vertex 服务账号与 AI Studio apikey/OAuth）。
// 统一使用原生 generateContent：prompt 与参考图以 inlineData 放入 parts。
func (e *CreativeExecutor) executeGemini(ctx context.Context, run CreativeRun, payload CreativeRunPayload, account *Account, upstreamModel string) ([]CreativeOutput, error) {
	if run.Operation == CreativeOperationInpaint {
		return nil, creativeNonRetryableError("gemini platform does not support creative operation %s", run.Operation)
	}
	if run.Operation != CreativeOperationGenerate && run.Operation != CreativeOperationEdit {
		return nil, creativeNonRetryableError("gemini platform does not support creative operation %s", run.Operation)
	}
	if e.gateway == nil {
		return nil, errors.New("creative gemini gateway is not configured")
	}
	request := buildCreativeGeminiRequest(run, payload, upstreamModel)
	options := gemininative.ImageOptions{Mode: gemininative.CredentialMode(account.Type), Model: upstreamModel, ProjectID: account.GetCredential("project_id"), APIKey: func() string { return account.GetCredential("api_key") }, BaseURL: func() string { return account.GetGeminiBaseURL(geminicli.AIStudioBaseURL) }, ValidateURL: e.gateway.validateUpstreamBaseURL, ValidateGeminiBaseURL: e.gateway.validateGeminiBaseURL, VertexURL: func() (string, error) {
		return buildVertexGeminiURL(account.VertexProjectID(), account.VertexLocation(upstreamModel), upstreamModel, "generateContent", false)
	}, ApplyHeaders: account.ApplyHeaderOverrides, Do: func(req *http.Request) (*http.Response, error) {
		return e.gateway.httpUpstream.Do(req, accountProxyURL(account), account.ID, account.Concurrency)
	}, HTTPError: func(status int, message string) error { return creativeHTTPStatusError(status, message) }, Invalid: func(format string, args ...any) error { return creativeNonRetryableError(format, args...) }, ErrorMessage: extractUpstreamErrorMessage, Enter: e.nativeAttemptActivity}
	if e.geminiTokens != nil {
		options.Token = func(ctx context.Context) (string, error) { return e.geminiTokens.GetAccessToken(ctx, account) }
	}
	return gemininative.GenerateImages(ctx, request, options)
}

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

func buildCreativeGeminiRequest(run CreativeRun, payload CreativeRunPayload, upstreamModel string) creativeGeminiGenerateRequest {
	input := gemininative.ImageRequestInput{Prompt: payload.Prompt, ImageSize: run.ImageSize, AspectRatio: run.AspectRatio, ThinkingLevel: payload.ThinkingLevel, Sources: make([]upstream.DecodedImage, len(payload.Sources))}
	for i, source := range payload.Sources {
		input.Sources[i] = upstream.DecodedImage{Bytes: source.Bytes, Mime: source.Mime}
	}
	return gemininative.BuildImageRequest(input)
}
