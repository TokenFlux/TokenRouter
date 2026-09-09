package service

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
)

const upstreamProtocolsKey = "upstream_protocols"

type clientProtocolContextKey struct{}

// WithClientProtocol 标记客户端业务入口；内部转换不能覆盖此标记。
func WithClientProtocol(ctx context.Context, protocol GroupClientProtocol) context.Context {
	return context.WithValue(ctx, clientProtocolContextKey{}, protocol)
}

func protocolAuthMode(account *Account) string {
	if account.IsOpenAIPersonalAccessToken() {
		return OpenAIAuthModePersonalAccessToken
	}
	if account.IsOpenAIAgentIdentity() {
		return OpenAIAuthModeAgentIdentity
	}
	return ""
}

// NativeProtocolOptions 复用认证模式判定，目录接口不接收任何实际凭据。
func (a *Account) NativeProtocolOptions() []GroupClientProtocol {
	if a == nil {
		return []GroupClientProtocol{}
	}
	return domain.NativeProtocolOptions(a.Platform, a.Type, protocolAuthMode(a))
}

func parseProtocolSet(raw any) ([]GroupClientProtocol, error) {
	out := []GroupClientProtocol{}
	switch value := raw.(type) {
	case []GroupClientProtocol:
		out = append(out, value...)
	case []string:
		for _, item := range value {
			out = append(out, GroupClientProtocol(item))
		}
	case []any:
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("upstream_protocols must contain strings")
			}
			out = append(out, GroupClientProtocol(text))
		}
	default:
		return nil, fmt.Errorf("upstream_protocols must be an array")
	}
	return out, nil
}

// UpstreamProtocols 读取统一结构；缺字段的旧记录只在兼容边界推导默认值。
func (a *Account) UpstreamProtocols() []GroupClientProtocol {
	if a == nil {
		return []GroupClientProtocol{}
	}
	if raw, exists := a.Credentials[upstreamProtocolsKey]; exists {
		protocols, err := parseProtocolSet(raw)
		if err != nil {
			return []GroupClientProtocol{}
		}
		return protocols
	}
	return a.legacyUpstreamProtocols()
}

func (a *Account) legacyUpstreamProtocols() []GroupClientProtocol {
	options := a.NativeProtocolOptions()
	if a.IsCNProvider() {
		switch a.GetAPIProtocol() {
		case APIProtocolAdaptive:
			return options
		case APIProtocolAnthropic:
			return []GroupClientProtocol{GroupClientProtocolAnthropicMessages}
		case APIProtocolResponses:
			return []GroupClientProtocol{GroupClientProtocolOpenAIResponses}
		default:
			return []GroupClientProtocol{GroupClientProtocolOpenAIChatCompletions}
		}
	}
	if a.IsOpenAIApiKey() {
		workloads, found := a.openAIWorkloadCapabilitySet()
		if found && !workloads["text_generation"] {
			options = slices.DeleteFunc(options, func(p GroupClientProtocol) bool {
				return p != domain.ProtocolEmbeddings && p != domain.ProtocolImagesGenerations && p != domain.ProtocolImagesEdits
			})
		}
		if found && !workloads["embeddings"] {
			options = slices.DeleteFunc(options, func(p GroupClientProtocol) bool { return p == domain.ProtocolEmbeddings })
		}
		mode := openai_compat.ResolveUpstreamTextProtocol(a.Extra, openai_compat.TextProtocolResponses)
		if mode == openai_compat.TextProtocolChatCompletions {
			options = slices.DeleteFunc(options, func(p GroupClientProtocol) bool { return p == GroupClientProtocolOpenAIResponses })
		}
		if openai_compat.ResolveUpstreamTextProtocol(a.Extra, openai_compat.TextProtocolChatCompletions) == openai_compat.TextProtocolResponses {
			options = slices.DeleteFunc(options, func(p GroupClientProtocol) bool { return p == GroupClientProtocolOpenAIChatCompletions })
		}
	}
	return options
}

// NormalizeAccountProtocols 是创建、编辑和导入的统一保存校验；空数组明确关闭新调用。
func NormalizeAccountProtocols(account *Account) error {
	if account == nil || account.IsCredentialShadow() {
		return nil
	}
	protocols := account.UpstreamProtocols()
	if raw, exists := account.Credentials[upstreamProtocolsKey]; exists {
		var err error
		protocols, err = parseProtocolSet(raw)
		if err != nil {
			return infraerrors.BadRequest("UPSTREAM_PROTOCOLS_INVALID", err.Error())
		}
	}
	options := account.NativeProtocolOptions()
	seen := map[GroupClientProtocol]bool{}
	for _, protocol := range protocols {
		if !slices.Contains(options, protocol) || seen[protocol] {
			return infraerrors.BadRequest("UPSTREAM_PROTOCOLS_INVALID", fmt.Sprintf("unsupported or duplicated native protocol %q", protocol))
		}
		seen[protocol] = true
	}
	normalized := []GroupClientProtocol{}
	for _, protocol := range options {
		if seen[protocol] {
			normalized = append(normalized, protocol)
		}
	}
	account.Credentials = maps.Clone(account.Credentials)
	if account.Credentials == nil {
		account.Credentials = map[string]any{}
	}
	// 固定 CN 端点迁入分协议地址，避免移除旧选项后改变自定义 base_url 的含义。
	if account.IsCNProvider() {
		legacy := account.GetCredential("api_protocol")
		if legacy != "" && legacy != APIProtocolAdaptive {
			urls, _ := account.Credentials["api_base_urls"].(map[string]any)
			urls = maps.Clone(urls)
			if urls == nil {
				urls = map[string]any{}
			}
			if base := strings.TrimSpace(account.GetCredential("base_url")); base != "" {
				if _, exists := urls[legacy]; !exists {
					urls[legacy] = base
				}
			}
			account.Credentials["api_base_urls"] = urls
		}
	}
	account.Credentials[upstreamProtocolsKey] = normalized
	delete(account.Credentials, "api_protocol")
	delete(account.Credentials, openAIWorkloadCapabilitiesCredentialKey)
	delete(account.Credentials, legacyOpenAICapabilitiesCredentialKey)
	account.Extra = maps.Clone(account.Extra)
	delete(account.Extra, openai_compat.ExtraKeyTextRouteMode)
	delete(account.Extra, legacyOpenAIResponsesModeExtraKey)
	return nil
}

// ResolveProtocolRoute 对每个候选独立解析，不产生隐式优先级或多级转换。
func ResolveProtocolRoute(account *Account, group *Group, source GroupClientProtocol) (GroupClientProtocol, bool) {
	if account == nil {
		return "", false
	}
	enabled := account.UpstreamProtocols()
	if slices.Contains(enabled, source) && slices.Contains(account.NativeProtocolOptions(), source) {
		return source, true
	}
	if source == domain.ProtocolImageBatches {
		// 批量作业沿用 provider 绑定，仅检查该 provider 的专用上游协议。
		target := domain.ProtocolGeminiBatch
		if account.Type == AccountTypeServiceAccount {
			target = domain.ProtocolVertexBatch
		}
		return target, account.Platform == PlatformGemini && slices.Contains(enabled, target)
	}
	if group == nil {
		return "", false
	}
	target := group.ProtocolFallbacks[source]
	if slices.Contains(enabled, target) && domain.SupportsProtocolConversion(account.Platform, account.Type, protocolAuthMode(account), source, target) {
		return target, true
	}
	return "", false
}

func (a *Account) allowsProtocolRequest(ctx context.Context) bool {
	source, _ := ctx.Value(clientProtocolContextKey{}).(GroupClientProtocol)
	if source == "" {
		return true
	}
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	_, ok := ResolveProtocolRoute(a, group, source)
	return ok
}

func normalizeGroupProtocolPolicy(group *Group) error {
	normalized, err := domain.ValidateGroupClientProtocols(group.Platform, group.AllowedProtocols)
	if err != nil {
		return infraerrors.BadRequest("GROUP_PROTOCOLS_INVALID", err.Error())
	}
	group.AllowedProtocols = normalized
	for source, target := range group.ProtocolFallbacks {
		if !slices.Contains(domain.ProtocolFallbackTargets(group.Platform, source), target) {
			return infraerrors.BadRequest("GROUP_PROTOCOL_FALLBACK_INVALID", fmt.Sprintf("unsupported conversion %s -> %s", source, target))
		}
	}
	if group.ProtocolFallbacks == nil {
		group.ProtocolFallbacks = map[GroupClientProtocol]GroupClientProtocol{}
	}
	switch group.ResponsesImagePolicy {
	case "":
		group.ResponsesImagePolicy = "inherit"
	case "inherit", "enabled", "disabled", "block":
	default:
		return infraerrors.BadRequest("GROUP_RESPONSES_IMAGE_POLICY_INVALID", "invalid Responses image policy")
	}
	// 旧服务仍读取这些派生值；它们不再作为独立配置写入。
	group.AllowMessagesDispatch = group.Platform == PlatformOpenAI && slices.Contains(normalized, GroupClientProtocolAnthropicMessages)
	group.AllowImageGeneration = slices.Contains(normalized, domain.ProtocolImagesGenerations) || slices.Contains(normalized, domain.ProtocolImagesEdits) || slices.Contains(normalized, domain.ProtocolImageBatches) || slices.Contains(normalized, GroupClientProtocolGeminiGenerateContent)
	group.AllowBatchImageGeneration = slices.Contains(normalized, domain.ProtocolImageBatches)
	group.AllowLive = slices.Contains(normalized, domain.ProtocolLive)
	return nil
}

// DefaultProtocolFallbacks 固化历史平台适配，管理员可显式清空映射改为仅原生。
func DefaultProtocolFallbacks(platform string) map[GroupClientProtocol]GroupClientProtocol {
	result := map[GroupClientProtocol]GroupClientProtocol{}
	target := GroupClientProtocolOpenAIResponses
	switch platform {
	case PlatformAnthropic:
		target = GroupClientProtocolAnthropicMessages
	case PlatformGemini, PlatformAntigravity:
		target = GroupClientProtocolGeminiGenerateContent
	case PlatformQoder:
		target = domain.ProtocolQoderChat
	case PlatformZhipu:
		target = GroupClientProtocolOpenAIChatCompletions
	}
	for _, source := range domain.SupportedGroupClientProtocols(platform) {
		if slices.Contains(domain.ProtocolFallbackTargets(platform, source), target) {
			result[source] = target
		}
	}
	if platform == PlatformAnthropic {
		result[GroupClientProtocolAnthropicMessages] = GroupClientProtocolGeminiGenerateContent
	}
	if slices.Contains(domain.ProtocolFallbackTargets(platform, GroupClientProtocolOpenAIResponses), GroupClientProtocolOpenAIChatCompletions) {
		result[GroupClientProtocolOpenAIResponses] = GroupClientProtocolOpenAIChatCompletions
	}
	return result
}

func applyLegacyGroupMediaProtocols(group *Group) {
	for protocol, enabled := range map[GroupClientProtocol]bool{
		domain.ProtocolImagesGenerations: group.AllowImageGeneration,
		domain.ProtocolImagesEdits:       group.AllowImageGeneration,
		domain.ProtocolImageBatches:      group.AllowBatchImageGeneration,
		domain.ProtocolLive:              group.AllowLive,
	} {
		if slices.Contains(domain.SupportedGroupClientProtocols(group.Platform), protocol) {
			group.AllowedProtocols = domain.SetGroupClientProtocol(group.AllowedProtocols, protocol, enabled)
		}
	}
}

// accountForProtocolAttempt 不修改共享账号或持久配置；每次切号都重新计算目标。
func accountForProtocolAttempt(ctx context.Context, account *Account) (*Account, error) {
	if account == nil {
		return nil, fmt.Errorf("account is nil")
	}
	if account.resolvedProtocol != "" {
		return account, nil
	}
	source, _ := ctx.Value(clientProtocolContextKey{}).(GroupClientProtocol)
	if source == "" {
		return account, nil
	}
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	target, ok := ResolveProtocolRoute(account, group, source)
	if !ok {
		return nil, fmt.Errorf("account %d has no enabled route for %s", account.ID, source)
	}
	copied := *account
	copied.resolvedProtocol = target
	return &copied, nil
}

func GroupAllowsResponsesImages(group *Group) bool {
	// 新配置由独立四态管理；旧内存对象保留历史权限语义以兼容内部调用。
	return group == nil || group.ResponsesImagePolicy != "" || group.AllowImageGeneration
}

func groupResponsesExplicitToolPolicy(group *Group, inherited string) string {
	if group == nil {
		return inherited
	}
	switch group.ResponsesImagePolicy {
	case "block":
		return codexImageGenerationExplicitToolPolicyStrip
	case "enabled", "disabled":
		return codexImageGenerationExplicitToolPolicyAllow
	default:
		return inherited
	}
}

func hasLegacyProtocolPatch(credentials, extra map[string]any) bool {
	for _, key := range []string{"api_protocol", openAIWorkloadCapabilitiesCredentialKey, legacyOpenAICapabilitiesCredentialKey} {
		if _, ok := credentials[key]; ok {
			return true
		}
	}
	for _, key := range []string{openai_compat.ExtraKeyTextRouteMode, legacyOpenAIResponsesModeExtraKey} {
		if _, ok := extra[key]; ok {
			return true
		}
	}
	return false
}

func applyLegacyProtocolPatch(account *Account, credentials, extra map[string]any) {
	if _, explicit := credentials[upstreamProtocolsKey]; explicit || !hasLegacyProtocolPatch(credentials, extra) {
		return
	}
	if _, unified := account.Credentials[upstreamProtocolsKey]; !unified {
		return
	}
	// 旧输入只覆盖原来的文本/工作负载维度，独立媒体原生集合保持不变。
	selected := account.UpstreamProtocols()
	legacy := *account
	legacy.Credentials = maps.Clone(account.Credentials)
	delete(legacy.Credentials, upstreamProtocolsKey)
	legacyProtocols := legacy.legacyUpstreamProtocols()
	isText := func(p GroupClientProtocol) bool {
		return p == GroupClientProtocolAnthropicMessages || p == GroupClientProtocolOpenAIResponses || p == GroupClientProtocolOpenAIChatCompletions
	}
	hasWorkload := false
	for _, key := range []string{openAIWorkloadCapabilitiesCredentialKey, legacyOpenAICapabilitiesCredentialKey} {
		if _, ok := credentials[key]; ok {
			hasWorkload = true
		}
	}
	selected = slices.DeleteFunc(selected, func(p GroupClientProtocol) bool { return isText(p) || (hasWorkload && p == domain.ProtocolEmbeddings) })
	for _, p := range legacyProtocols {
		if isText(p) || (hasWorkload && p == domain.ProtocolEmbeddings) {
			selected = append(selected, p)
		}
	}
	account.Credentials = maps.Clone(account.Credentials)
	account.Credentials[upstreamProtocolsKey] = selected
}

func responsesPolicyGroup(ctx context.Context, group *Group) *Group {
	source, _ := ctx.Value(clientProtocolContextKey{}).(GroupClientProtocol)
	if source != "" && source != GroupClientProtocolOpenAIResponses && source != domain.ProtocolResponsesWebSocket {
		return nil
	}
	return group
}

func (s *OpenAIGatewayService) shadowProtocolsAllowed(ctx context.Context, account *Account) bool {
	if account == nil || !account.IsShadow() {
		return true
	}
	if source, _ := ctx.Value(clientProtocolContextKey{}).(GroupClientProtocol); source == "" {
		return true
	}
	parent := s.parentAccountLookup(ctx)(*account.ParentAccountID)
	return parent != nil && parent.allowsProtocolRequest(ctx)
}

func supportsOpenAIRequestCapability(ctx context.Context, account *Account, capability OpenAIEndpointCapability) bool {
	if account == nil {
		return false
	}
	source, _ := ctx.Value(clientProtocolContextKey{}).(GroupClientProtocol)
	if source == domain.ProtocolResponsesWebSocket || source == domain.ProtocolResponsesCompact {
		if capability == OpenAIEndpointCapabilityTextGeneration || capability == OpenAIEndpointCapabilityResponses {
			return account.allowsProtocolRequest(ctx)
		}
		if capability == OpenAIEndpointCapabilityRemoteCompactionV2 {
			return account.allowsProtocolRequest(ctx) && account.AllowsOpenAINativeCompactionV2()
		}
	}
	return account.SupportsOpenAIEndpointCapability(capability)
}

// 创作台复用相同业务协议；已创建任务的读取与清理不经过此准入。
func creativeOperationProtocol(platform, operation string) GroupClientProtocol {
	if platform == PlatformGemini {
		return GroupClientProtocolGeminiGenerateContent
	}
	if operation == CreativeOperationGenerate {
		return domain.ProtocolImagesGenerations
	}
	return domain.ProtocolImagesEdits
}

func creativeOperationsForGroup(group *Group) []string {
	operations := creativeOperationsForPlatform(group.Platform)
	if group.ResponsesImagePolicy == "" && group.ProtocolFallbacks == nil {
		return operations
	}
	return slices.DeleteFunc(operations, func(operation string) bool {
		return !group.AllowsClientProtocol(creativeOperationProtocol(group.Platform, operation))
	})
}

func preserveProtocolCredentials(existing, incoming map[string]any) map[string]any {
	out := maps.Clone(incoming)
	if out == nil {
		out = map[string]any{}
	}
	if !hasLegacyProtocolPatch(incoming, nil) {
		for _, key := range []string{upstreamProtocolsKey, "api_base_urls"} {
			if _, supplied := out[key]; !supplied {
				if value, exists := existing[key]; exists {
					out[key] = value
				}
			}
		}
	}
	return out
}
