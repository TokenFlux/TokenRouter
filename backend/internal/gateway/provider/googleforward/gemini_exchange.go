package googleforward

import (
	"context"
	"net/http"
	"strings"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

type geminiExchangeMode int

const (
	geminiExchangeMessages geminiExchangeMode = iota
	geminiExchangeNative
	geminiExchangeOpenAI
)

func (s *Gemini) geminiExchangeOptions(c *attempt, ctx context.Context, account *gatewayprovider.ExecutionAccount, model string, mode geminiExchangeMode, protocol gemininative.OpenAICompatProtocol) gemininative.ExchangeOptions {
	options := gemininative.ExchangeOptions{
		AccountID:   account.Record.ID,
		AccountName: account.Record.Name,
		Platform:    account.Record.Platform,
		MaxRetries:  geminiMaxRetries,
		ReadError:   s.readUpstreamErrorBody,
		Sanitize:    logredact.SanitizeUpstreamQueries,
		Message:     upstream.ExtractErrorMessage,
		CheckPolicy: func(ctx context.Context, resp *http.Response) (bool, *http.Response) {
			return s.checkErrorPolicyInLoop(ctx, account, resp, model)
		},
		ShouldRetry: func(code int) bool { return s.shouldRetryGeminiUpstreamError(account, code) },
		OnStatus: func(ctx context.Context, status int, header http.Header, body []byte) {
			s.observeHealth(ctx, account, status, header, body)
		},
		Observe: func(value gemininative.ExchangeNotice) {
			c.Observe(ops.OpsUpstreamErrorEvent{
				Platform:           value.Platform,
				AccountID:          value.AccountID,
				AccountName:        value.AccountName,
				UpstreamStatusCode: value.UpstreamStatusCode,
				UpstreamRequestID:  value.UpstreamRequestID,
				Kind:               value.Kind,
				Message:            value.Message,
				Detail:             value.Detail,
			})
		},
		SetError: func(code int, message, detail string) { c.SetError(code, message, detail) },
		Detail:   s.upstreamErrorDetail,
	}
	options.BuildError = func(err error) error {
		if mode == geminiExchangeOpenAI {
			return c.GeminiOpenAICompatError(protocol, http.StatusBadGateway, "upstream_error", err.Error())
		}
		status := http.StatusBadGateway
		kind := "upstream_error"
		if strings.Contains(err.Error(), "missing project_id") {
			status = http.StatusBadRequest
			kind = "invalid_request_error"
		}
		if mode == geminiExchangeNative {
			return c.GoogleError(status, err.Error())
		}
		return c.ClaudeError(status, kind, err.Error())
	}
	options.FinalError = func(message string) error {
		if mode == geminiExchangeNative {
			return c.GoogleError(http.StatusBadGateway, message)
		}
		if mode == geminiExchangeOpenAI {
			return c.GeminiOpenAICompatError(protocol, http.StatusBadGateway, "upstream_error", message)
		}
		return c.ClaudeError(http.StatusBadGateway, "upstream_error", message)
	}
	return options
}
