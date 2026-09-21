//go:build unit

package httpapi

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
)

func executeOpenAIProbeRequestType(t *testing.T, executor *provider.OpenAIAccountTest, output *openAIProbeOutput, id int64, model, prompt, kind, mode, protocol string) error {
	t.Helper()
	return openAIProbeCore(executor).Test(output.Request.Context(), account.TestRequest{AccountID: id, Model: model, Prompt: prompt, Mode: mode, Type: &kind, Protocol: protocol}, NewTestEventSink(output.recorder))
}
