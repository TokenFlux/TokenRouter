//go:build unit

package httpapi

import (
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// grokMediaFixture 只装配媒体场景使用的固定依赖。
func grokMediaFixture(transport httpclient.UpstreamTransport) *GrokExecutor {
	return &GrokExecutor{Credentials: testkit.RequestCredentials(nil, nil, nil, nil), Transport: transport, Output: &OpenAIResponseOutput{Options: OpenAIResponseOptions{ReadLimit: 128 * 1024 * 1024}}, Health: &accountprovider.GrokHealth{}, Routes: provider.GrokRoutes{Validate: (egress.OperatorURLPolicy{}).Validate}, Failure: &UpstreamTransportFailure{}}
}
