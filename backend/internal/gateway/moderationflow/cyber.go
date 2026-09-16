// moderationflow 只组织网关审核观测与完成记录；处置规则由 moderation 唯一拥有。
package moderationflow

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

type Mark struct {
	Code, Message, Body                           string
	UpstreamStatus, UpstreamInTok, UpstreamOutTok int
}
type Account struct {
	ID             int64
	Name, Platform string
}

const SessionBlockedClientMessage = "该会话已被网络安全策略屏蔽，请开启新会话 / This session is blocked by cyber-security policy, please start a new session"

func cloneID(in *int64) *int64 {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

type BlockWriter interface {
	MarkCyberSessionBlocked(context.Context, string, []string)
}
type Tasks interface{ Go(string, func()) bool }
type OpsWriter interface {
	Enqueue(*ops.OpsInsertErrorLogInput)
}
type Runtime struct {
	Recorder *completion.Recorder
	Blocks   BlockWriter
	Tasks    Tasks
	Ops      OpsWriter
}
type PolicyCompletion struct {
	Usage          *completion.Input
	Meta           OpsMeta
	Mark           Mark
	ForwardErrored bool
	BlockKey       string
}

// Dispatch 在提交屏障前拍快照，闭包只持有独立输入和固定依赖。
func (r Runtime) Dispatch(ctx context.Context, in PolicyCompletion) {
	in.Usage = completion.Snapshot(in.Usage)
	in.Meta.GroupID = cloneID(in.Meta.GroupID)
	source := completion.SnapshotContext(ctx)
	r.Tasks.Go("handler/openai_gateway_handler.go:recordCyberPolicyIfMarked", func() {
		work, cancel := context.WithTimeout(source, 30*time.Second)
		defer cancel()
		if in.ForwardErrored && r.Recorder != nil {
			r.Recorder.RecordCyber(work, in.Usage)
		}
		if r.Blocks != nil && in.BlockKey != "" {
			r.Blocks.MarkCyberSessionBlocked(work, "", []string{in.BlockKey})
		}
		if r.Ops != nil {
			r.Ops.Enqueue(BuildPolicyOpsEntry(in.Meta, &in.Mark))
		}
	})
}

// SnapshotContent 保留当前轮多模态快照，并隔离所有可变切片。
func SnapshotContent(in moderation.ContentModerationInput) moderation.ContentModerationInput {
	in.Images = slices.Clone(in.Images)
	in.Items = slices.Clone(in.Items)
	in.ImageItems = slices.Clone(in.ImageItems)
	return in
}

// BlockPlan 只携带已解析的会话键，解析与 Redis 的既有实现继续各自复用。
type BlockPlan struct {
	ScopeKey string
	Keys     []string
}

func BuildBlockPlan(explicit string, transcript []string, scope string) BlockPlan {
	p := BlockPlan{}
	if explicit != "" {
		p.Keys = append(p.Keys, explicit)
	}
	for _, key := range transcript {
		if len(p.Keys) == 0 || key != p.Keys[0] {
			p.Keys = append(p.Keys, key)
		}
	}
	if len(transcript) > 0 {
		p.ScopeKey = scope
	}
	return p
}
func (r Runtime) MarkBeforeScope(plan BlockPlan) {
	if len(plan.Keys) == 0 || r.Blocks == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	r.Blocks.MarkCyberSessionBlocked(ctx, plan.ScopeKey, slices.Clone(plan.Keys))
}

type OpsMeta struct {
	RequestID        string
	ClientRequestID  string
	Platform         string
	Model            string
	RequestPath      string
	Stream           bool
	InboundEndpoint  string
	UpstreamEndpoint string
	UserAgent        string
	APIKeyPrefix     string
	UserID           int64
	APIKeyID         int64
	AccountID        int64
	GroupID          *int64
	ClientIP         string
	CreatedAt        time.Time
	SessionBlockKey  string
}

func BuildPolicyOpsEntry(meta OpsMeta, mark *Mark) *ops.OpsInsertErrorLogInput {
	if mark == nil {
		return nil
	}
	rt := int16(usage.RequestTypeCyberBlocked)
	entry := &ops.OpsInsertErrorLogInput{
		RequestID:         meta.RequestID,
		ClientRequestID:   meta.ClientRequestID,
		Platform:          meta.Platform,
		Model:             meta.Model,
		RequestPath:       meta.RequestPath,
		Stream:            meta.Stream,
		InboundEndpoint:   meta.InboundEndpoint,
		UpstreamEndpoint:  meta.UpstreamEndpoint,
		RequestedModel:    meta.Model,
		RequestType:       &rt,
		UserAgent:         meta.UserAgent,
		APIKeyPrefix:      meta.APIKeyPrefix,
		ErrorPhase:        "request",
		ErrorType:         "cyber_policy",
		Severity:          "P3",
		StatusCode:        mark.UpstreamStatus,
		IsBusinessLimited: true,
		ErrorMessage:      "cyber_policy: " + strings.TrimSpace(mark.Message),
		ErrorBody:         mark.Body,
		ErrorSource:       "upstream_http",
		ErrorOwner:        "provider",
		CreatedAt:         meta.CreatedAt,
	}
	if meta.UserID > 0 {
		entry.UserID = &meta.UserID
	}
	if meta.APIKeyID > 0 {
		entry.APIKeyID = &meta.APIKeyID
	}
	if meta.AccountID > 0 {
		entry.AccountID = &meta.AccountID
	}
	if meta.GroupID != nil {
		entry.GroupID = cloneID(meta.GroupID)
	}
	if meta.ClientIP != "" {
		entry.ClientIP = &meta.ClientIP
	}
	return entry
}
func BuildSessionBlockedOpsEntry(meta OpsMeta) *ops.OpsInsertErrorLogInput {
	rt := int16(usage.RequestTypeCyberBlocked)
	entry := &ops.OpsInsertErrorLogInput{
		RequestID:         meta.RequestID,
		ClientRequestID:   meta.ClientRequestID,
		Platform:          meta.Platform,
		Model:             meta.Model,
		RequestPath:       meta.RequestPath,
		Stream:            meta.Stream,
		InboundEndpoint:   meta.InboundEndpoint,
		RequestedModel:    meta.Model,
		RequestType:       &rt,
		UserAgent:         meta.UserAgent,
		APIKeyPrefix:      meta.APIKeyPrefix,
		ErrorPhase:        "request",
		ErrorType:         "cyber_policy_session_blocked",
		Severity:          "P3",
		StatusCode:        403,
		IsBusinessLimited: true,
		ErrorMessage:      "cyber_policy_session_blocked: request rejected locally by session block",
		ErrorBody:         "session_block_key=" + meta.SessionBlockKey,
		ErrorSource:       "gateway_local",
		ErrorOwner:        "platform",
		CreatedAt:         meta.CreatedAt,
	}
	if meta.UserID > 0 {
		entry.UserID = &meta.UserID
	}
	if meta.APIKeyID > 0 {
		entry.APIKeyID = &meta.APIKeyID
	}
	if meta.GroupID != nil {
		entry.GroupID = cloneID(meta.GroupID)
	}
	if meta.ClientIP != "" {
		entry.ClientIP = &meta.ClientIP
	}
	return entry
}
