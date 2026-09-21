// 此处只投影现有账号凭据能力与平台原语，不拥有账号业务规则。
package provider

import (
	"context"
	"io"
	"strings"

	acct "github.com/TokenFlux/TokenRouter/internal/account"
	core "github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

type Account = acct.Record
type GeminiTokenCache = acct.AccessTokenCache

const PlatformGemini = acct.PlatformGemini
const AccountTypeAPIKey = acct.AccountTypeAPIKey
const AccountTypeServiceAccount = acct.AccountTypeServiceAccount

type BatchImageProvider interface {
	Name() string
	SupportsAccount(*Account) bool
	Submit(context.Context, *core.BatchImageJob, *Account, core.BatchImageInput) (*core.BatchProviderJob, error)
	Get(context.Context, *core.BatchImageJob, *Account) (*core.BatchProviderStatus, error)
	Cancel(context.Context, *core.BatchImageJob, *Account) error
	OpenResult(context.Context, *core.BatchImageJob, *Account) (io.ReadCloser, string, error)
	Cleanup(context.Context, *core.BatchImageJob, *Account, core.CleanupTarget) error
}

func batchImageProviderAPIKey(value *Account) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(value.GetCredential("api_key"))
}
func resolveBatchProtocol(value *Account) (capability.ProtocolID, bool) {
	plan := routing.Plan(routing.PlanInput{ClientProtocol: capability.ProtocolImageBatches})
	candidate, ok := plan.ResolveCandidate(value.RoutingSnapshot())
	return candidate.UpstreamProtocol, ok
}

func vertexProjectID(value *Account) string {
	return value.VertexProjectID(func(raw []byte) (string, error) {
		key, err := vertex.ParseVertexServiceAccountJSON(raw)
		if err != nil {
			return "", err
		}
		return key.ProjectID, nil
	})
}
