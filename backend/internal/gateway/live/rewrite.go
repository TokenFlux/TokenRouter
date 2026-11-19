package live

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/routing/modelmap"
)

// rewrite.go rewrites Live session model declarations using saved Key
// redirects and current routing resolution.
// Later Live session declarations need the API Key redirect snapshot saved at
// creation as well as current group and provider model resolution. The
// rewritten upstream frame must retain the original client model so the return
// direction can restore that name after internal routing.
//
// Additional tool models inside session are redirected independently before
// the main session.model is resolved. When the client repeats the saved
// requested name and no Key rule matches, the saved creation model supplies
// its internal target. Positive stored group identity is passed to the
// resolver, and an empty upstream mapping is rejected. Errors return the
// supplied payload rather than a partly rewritten frame; declarations with no
// main model can still retain successful tool-model changes.

// ModelResolver resolves model mappings for the current provider and final group.
type ModelResolver interface {
	// ResolveModel returns group-routing and provider-upstream names for the Key
	// target model. A nil group represents no positive stored group identifier;
	// resolution errors stop the entire client-frame rewrite.
	ResolveModel(context.Context, *int64, string) (string, string, error)
}

// RewriteClientPayload returns the routed client frame, original client label,
// and internal names used for restoration. Nil record or resolver passes
// through; malformed, non-object, duplicate-key, or case-ambiguous JSON returns
// an error, while a missing session.model returns any completed additional-tool
// rewrite with empty model evidence.
//
// The saved Key mapping applies one hop before current group/provider
// resolution. A repeated requested name can reuse the stored creation model
// when no rule matches. Empty upstream names are rejected, and every rewrite
// or resolution error returns the supplied payload so the proxy does not
// forward partially changed routing. Successful model evidence preserves Key,
// group, and upstream stages separately.
func RewriteClientPayload(
	ctx context.Context,
	record *session.LiveCallRecord,
	resolve ModelResolver,
	payload []byte,
) ([]byte, string, []string, error) {
	if record == nil || resolve == nil {
		return payload, "", nil, nil
	}
	if err := wirejson.ValidateRequestObject(payload); err != nil {
		return payload, "", nil, err
	}
	rewritten := payload
	// Redirect nested tool model declarations with the same creation-time Key
	// rules before resolving the main model, keeping auxiliary upstream choices
	// aligned with that saved snapshot.
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
		// Reuse the creation target when the saved client alias repeats without a
		// matching Key rule, retaining its earlier redirection before current group
		// and provider resolution.
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
