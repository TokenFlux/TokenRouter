package live

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/routing/modelmap"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ModelResolver 只投影当前账号与最终分组的模型链。
type ModelResolver interface {
	ResolveModel(context.Context, *int64, string) (string, string, error)
}

// rewriteLiveSidebandClientPayload 对每轮 session.model 执行 Key、分组和账号映射。
func RewriteClientPayload(
	ctx context.Context,
	record *session.LiveCallRecord,
	resolve ModelResolver,
	payload []byte,
) ([]byte, string, []string, error) {
	if record == nil || resolve == nil || !gjson.ValidBytes(payload) {
		return payload, "", nil, nil
	}
	rewritten := payload
	if session := gjson.GetBytes(rewritten, "session"); session.IsObject() && len(record.APIKeyModelMapping) > 0 {
		rewrittenSession, err := modeltrace.RewriteAPIKeyAdditionalModels([]byte(session.Raw), record.APIKeyModelMapping)
		if err != nil {
			return payload, "", nil, err
		}
		if !bytes.Equal(rewrittenSession, []byte(session.Raw)) {
			rewritten, err = sjson.SetRawBytes(rewritten, "session", rewrittenSession)
			if err != nil {
				return payload, "", nil, err
			}
		}
	}
	clientModel := strings.TrimSpace(gjson.GetBytes(rewritten, "session.model").String())
	if clientModel == "" {
		return rewritten, "", nil, nil
	}
	keyTarget := clientModel
	if mappedModel, matched := modelmap.Resolve(record.APIKeyModelMapping, clientModel); matched {
		keyTarget = mappedModel
	} else if clientModel == strings.TrimSpace(record.RequestedModel) && strings.TrimSpace(record.Model) != "" {
		keyTarget = strings.TrimSpace(record.Model)
	}
	var groupID *int64
	if record.GroupID > 0 {
		value := record.GroupID
		groupID = &value
	}
	routingModel, upstreamModel, err := resolve.ResolveModel(ctx, groupID, keyTarget)
	if err != nil {
		return payload, "", nil, err
	}
	upstreamModel = strings.TrimSpace(upstreamModel)
	if upstreamModel == "" {
		return payload, "", nil, fmt.Errorf("live session model %s has no upstream mapping", clientModel)
	}
	rewritten, err = sjson.SetBytes(rewritten, "session.model", upstreamModel)
	if err != nil {
		return payload, "", nil, err
	}
	return rewritten, clientModel, []string{keyTarget, routingModel, upstreamModel}, nil
}
