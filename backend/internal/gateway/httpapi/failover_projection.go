package httpapi

import (
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
)

// ProjectOpenAIFailoverError 只投影平台已确认的展示信息，规则解释在新 HTTP 层。
func ProjectOpenAIFailoverError(err *forwardcore.UpstreamFailoverError) *OpenAIFailoverError {
	if err == nil {
		return nil
	}
	credentialStatus, credentialMessage := CredentialFailoverClientResponse(err)
	return &OpenAIFailoverError{
		Status:                  err.StatusCode,
		ClientStatus:            err.ClientStatusCode,
		ClientMessage:           err.ClientMessage,
		CredentialStatus:        credentialStatus,
		CredentialMessage:       credentialMessage,
		Headers:                 err.ResponseHeaders,
		Body:                    err.ResponseBody,
		TooLarge:                gatewayprovider.IsOpenAIRequestBodyTooLarge(err),
		TooLargeMessage:         forwardcore.OpenAIRequestBodyTooLargeClientMessage,
		ContinuationUnsupported: err.Reason == forwardcore.OpenAIHTTPContinuationUnsupportedReason,
		Credential:              err.IsCredentialFailure(),
		CapacityShed:            gatewayprovider.IsOpenAICapacityShed(err),
		SilentRefusal:           forwardcore.IsOpenAISilentRefusalErrorBody(err.ResponseBody),
		SilentMessage:           forwardcore.OpenAISilentRefusalClientMessage(),
		CyberWarning:            gatewayprovider.IsOpenAICyberWarningPayload(err.ResponseBody, ""),
		CyberMessage:            gatewayprovider.ExtractOpenAICyberWarningMessage(err.ResponseBody, ""),
		UpstreamMessage:         upstream.ExtractErrorMessage(err.ResponseBody),
	}
}
