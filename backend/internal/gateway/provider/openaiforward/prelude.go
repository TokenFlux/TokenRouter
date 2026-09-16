// 请求准备按原时点推进，技术兼容转换与账号策略通过明确端口组合。
package openaiforward

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type Dispatch uint8

const (
	DispatchResponses Dispatch = iota
	DispatchRawChat
	DispatchGrok
	DispatchAnthropic
	DispatchPassthrough
)

type TransportDecision struct{ Transport, Reason string }

// Profile 只保存当前账号已经确认的协议资格；不携带凭据或可变账号实体。
type Profile struct {
	Platform, Name, Type                                                    string
	UsesCodex                                                               bool
	OpenAI, OAuth, OAuthLike, APIKey                                        bool
	Grok, DeepSeek, NativeCN, Anthropic, RawChat, ResolvedChat, Passthrough bool
}
type Rejection struct {
	Status                                       int
	Type, Message, Param                         string
	PolicyDenied, FeatureDenied, ObserveUpstream bool
}
type Prelude struct {
	Body, OriginalBody, CanonicalImageIntentBody              []byte
	StartedAt                                                 time.Time
	TLS                                                       egress.TLSFingerprintRouterMatchResult
	Transport                                                 TransportDecision
	Compact, MessagesBridge, CodexCLI, ImageIntentInvalidated bool
	ImageToolPolicy                                           string
	ReasoningEffort                                           *string
	View                                                      requeststate.OpenAIRequestView
	Route                                                     Dispatch
}

// PreludePorts 不执行完整转发；每个端口仅作一次策略读取、纯报文转换或观察。
type PreludePorts interface {
	BlockGroupImages() bool
	StripImages([]byte) ([]byte, bool, error)
	Begin()
	FilterNoneReasoning([]byte) ([]byte, error)
	ClearMappings()
	PrepareIdentity(context.Context) error
	MatchTLS() egress.TLSFingerprintRouterMatchResult
	ClientAllowed(context.Context, egress.TLSFingerprintRouterMatchResult, []byte) (bool, string)
	Reject(Rejection)
	CompactEffort([]byte) ([]byte, bool, error)
	ToolSchemas([]byte) ([]byte, bool, error)
	LiteHeader() bool
	LitePayload([]byte) ([]byte, bool, string, error)
	Transport() TransportDecision
	CompactPath() bool
	CompactBody([]byte) ([]byte, bool, error)
	CompactAPIKeyReplay([]byte) ([]byte, bool, error)
	FlattenRequired(TransportDecision, bool, bool) bool
	Flatten([]byte) ([]byte, error)
	StripNamespacesRequired(TransportDecision, bool) bool
	KeepNamespaces(TransportDecision, bool, bool, []byte) bool
	StripNamespaces([]byte, bool) ([]byte, error)
	NeedsClientTools([]byte) bool
	AdaptClientTools([]byte) ([]byte, error)
	ValidateEffort([]byte, string) error
	ReasoningReplay([]byte) ([]byte, bool, error)
	InputItemIDs([]byte) ([]byte, bool, error)
	MessagesBridge([]byte) bool
	BindMessagesBridge(bool)
	CodexClient() bool
	ImageToolPolicy() string
	ObserveTransport(TransportDecision, string, bool)
	MappedModel(string) string
	PassthroughEffort([]byte, string) *string
	Log(string, ...any)
}

// PreparePrelude 保留 Grok、原生 Messages、Raw Chat 和透传的短路顺序。
func PreparePrelude(ctx context.Context, body []byte, profile Profile, p PreludePorts) (*Prelude, error) {
	var err error
	if p.BlockGroupImages() {
		body, _, err = p.StripImages(body)
		if err != nil {
			return nil, err
		}
	}
	p.Begin()
	body, err = p.FilterNoneReasoning(body)
	if err != nil {
		return nil, err
	}
	p.ClearMappings()
	if err = p.PrepareIdentity(ctx); err != nil {
		return nil, err
	}
	value := &Prelude{StartedAt: time.Now(), CanonicalImageIntentBody: body, ImageToolPolicy: "allow"}
	value.TLS = p.MatchTLS()
	if allowed, message := p.ClientAllowed(ctx, value.TLS, body); !allowed {
		p.Reject(Rejection{Status: 403, Type: "forbidden_error", Message: message, PolicyDenied: true})
		return nil, errors.New("openai oauth client policy restriction: client is not allowed")
	}
	if updated, changed, err := p.CompactEffort(body); err != nil {
		return nil, err
	} else if changed {
		body = updated
	}
	if updated, changed, err := p.ToolSchemas(body); err != nil {
		return nil, err
	} else if changed {
		body = updated
	}
	if profile.OpenAI && profile.OAuth {
		if updated, changed, err := native.NormalizeOpenAIResponsesReasoningMode(body); err != nil {
			return nil, fmt.Errorf("normalize OpenAI Responses reasoning.mode: %w", err)
		} else if changed {
			body = updated
		}
	}
	if profile.OpenAI && p.LiteHeader() {
		updated, changed, param, err := p.LitePayload(body)
		if err != nil {
			p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: err.Error(), Param: param, ObserveUpstream: true})
			return nil, err
		}
		if changed {
			body = updated
		}
	}
	value.Transport = p.Transport()
	value.Compact = p.CompactPath()
	if value.Compact {
		if updated, changed, err := p.CompactBody(body); err != nil {
			return nil, err
		} else if changed {
			body = updated
		}
		if profile.OpenAI && profile.APIKey {
			if updated, changed, err := p.CompactAPIKeyReplay(body); err != nil {
				return nil, err
			} else if changed {
				body = updated
			}
		}
	}
	if p.FlattenRequired(value.Transport, profile.Passthrough, value.Compact) {
		body, err = p.Flatten(body)
		if err != nil {
			p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: err.Error(), Param: "tools", ObserveUpstream: true})
			return nil, err
		}
	}
	if p.StripNamespacesRequired(value.Transport, profile.Passthrough) {
		keep := p.KeepNamespaces(value.Transport, profile.Passthrough, value.Compact, body)
		body, err = p.StripNamespaces(body, keep)
		if err != nil {
			p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: err.Error(), Param: "input", ObserveUpstream: true})
			return nil, err
		}
	}
	if profile.DeepSeek && profile.NativeCN && profile.APIKey && !value.Compact && p.NeedsClientTools(body) {
		body, err = p.AdaptClientTools(body)
		if err != nil {
			return nil, fmt.Errorf("adapt DeepSeek Responses client tools: %w", err)
		}
	}
	value.Body = body
	value.OriginalBody = body
	value.View = requeststate.NewOpenAIRequestView(body)
	if profile.Grok {
		value.Route = DispatchGrok
		if profile.ResolvedChat {
			value.Route = DispatchRawChat
		}
		return value, nil
	}
	if err := p.ValidateEffort(body, value.View.Model); err != nil {
		p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: err.Error(), Param: "reasoning.effort"})
		return nil, err
	}
	if profile.Anthropic {
		value.Route = DispatchAnthropic
		return value, nil
	}
	if profile.RawChat {
		value.Route = DispatchRawChat
		return value, nil
	}
	if profile.OpenAI && (profile.APIKey || profile.OAuthLike) {
		if updated, changed, err := p.ReasoningReplay(body); err != nil {
			return nil, fmt.Errorf("normalize OpenAI Responses reasoning content replay: %w", err)
		} else if changed {
			body = updated
			value.Body = body
			value.OriginalBody = body
			value.View = requeststate.NewOpenAIRequestView(body)
		}
		if updated, changed, err := p.InputItemIDs(body); err != nil {
			return nil, fmt.Errorf("sanitize OpenAI Responses input item IDs: %w", err)
		} else if changed {
			body = updated
			value.Body = body
			value.OriginalBody = body
			value.View = requeststate.NewOpenAIRequestView(body)
		}
	}
	value.MessagesBridge = p.MessagesBridge(body)
	p.BindMessagesBridge(value.MessagesBridge)
	value.CodexCLI = p.CodexClient()
	if value.CodexCLI {
		value.ImageToolPolicy = p.ImageToolPolicy()
	}
	p.ObserveTransport(value.Transport, value.View.Model, value.View.Stream)
	if value.Transport.Transport == "responses_websockets" {
		p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: "OpenAI WSv1 is temporarily unsupported. Please enable responses_websockets_v2.", FeatureDenied: true})
		return nil, errors.New("openai ws v1 is temporarily unsupported; use ws v2")
	}
	if profile.Passthrough {
		if value.ImageToolPolicy == "strip" {
			if updated, changed, err := p.StripImages(body); err != nil {
				return nil, err
			} else if changed {
				body = updated
				value.Body = body
				value.OriginalBody = body
				value.ImageIntentInvalidated = true
				p.Log("[OpenAI] Stripped /responses image_generation tool for Codex client by account policy")
			}
		}
		value.ReasoningEffort = p.PassthroughEffort(body, p.MappedModel(value.View.Model))
		value.Route = DispatchPassthrough
	}
	return value, nil
}
