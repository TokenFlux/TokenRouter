package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// 创作台执行器常量。
const (
	defaultCreativeExecuteTimeout = 5 * time.Minute
	defaultCreativeMaxAttempts    = 3
	// creativeMaxOutputBytes 限制单张输出图片大小（32 MiB）。
	creativeMaxOutputBytes = 32 << 20
)

// CreativeUpstreamError 是执行器向上抛出的上游调用错误，携带可重试判定。
type CreativeUpstreamError struct {
	StatusCode int
	Message    string
	Retryable  bool
}

func (e *CreativeUpstreamError) Error() string {
	if e == nil {
		return "creative upstream error"
	}
	return fmt.Sprintf("creative upstream error status=%d: %s", e.StatusCode, e.Message)
}

// creativeNonRetryableError 生成不可重试的执行错误（如平台不支持该操作）。
func creativeNonRetryableError(format string, args ...any) *CreativeUpstreamError {
	return &CreativeUpstreamError{StatusCode: 0, Message: fmt.Sprintf(format, args...), Retryable: false}
}

// creativeHTTPStatusError 把上游 HTTP 状态码映射为可重试性：网络层错误、429 与 5xx 可重试，其余 4xx 不可重试。
func creativeHTTPStatusError(statusCode int, message string) *CreativeUpstreamError {
	retryable := statusCode == 0 || statusCode == http.StatusTooManyRequests || statusCode >= 500
	// 上游 body 可能回显 prompt、内部 URL 或认证信息；对外只保留稳定的状态类消息。
	publicMessage := "creative provider request failed"
	switch {
	case statusCode == 0:
		publicMessage = "creative provider connection failed"
	case statusCode == http.StatusTooManyRequests:
		publicMessage = "creative provider rate limited"
	case statusCode >= 500:
		publicMessage = "creative provider unavailable"
	case statusCode >= 400:
		publicMessage = "creative provider rejected request"
	}
	return &CreativeUpstreamError{StatusCode: statusCode, Message: publicMessage, Retryable: retryable}
}

// IsRetryableCreativeError 判断错误是否值得 worker 有限重试。
func IsRetryableCreativeError(err error) bool {
	var upstreamErr *CreativeUpstreamError
	if errors.As(err, &upstreamErr) {
		return upstreamErr.Retryable
	}
	// 未知错误（网络层、序列化等）保守视为可重试，由最大次数兜底。
	return err != nil
}

// CreativeExecutor 是创作台的 provider 执行器：按分组平台直接构造上游 HTTP 请求，
// 绝不通过本地 HTTP 回环调用网关。
type CreativeExecutor struct {
	nativeAttemptActivity func() (func(), error)
	cfg                   *config.Config
	accountRepo           CreativeAccountRepository
	groupRepo             CreativeGroupRepository
	gateway               *OpenAIGatewayService
	gatewayService        *GatewayService
	geminiTokens          *GeminiTokenProvider
	settingService        *SettingService
}

// NewCreativeExecutor 创建创作台执行器。
func NewCreativeExecutor(
	cfg *config.Config,
	accountRepo CreativeAccountRepository,
	groupRepo CreativeGroupRepository,
	gateway *OpenAIGatewayService,
	gatewayService *GatewayService,
	geminiTokens *GeminiTokenProvider,
	settingService *SettingService,
) *CreativeExecutor {
	return &CreativeExecutor{
		cfg:            cfg,
		accountRepo:    accountRepo,
		groupRepo:      groupRepo,
		gateway:        gateway,
		gatewayService: gatewayService,
		geminiTokens:   geminiTokens,
		settingService: settingService,
	}
}

// Prepare 复用现有网关调度器选择账号并预占账号并发槽位。
func (e *CreativeExecutor) Prepare(ctx context.Context, run CreativeRun) (*CreativeExecution, error) {
	if e == nil {
		return nil, errors.New("creative executor is not configured")
	}
	platform, err := e.resolveGroupPlatform(ctx, run.GroupID)
	if err != nil {
		return nil, err
	}
	// 新调度仍使用该操作的协议资格；任务资源管理不受创建入口开关影响。
	if group, loadErr := e.groupRepo.GetByIDLite(ctx, run.GroupID); loadErr == nil && group != nil && (group.ResponsesImagePolicy != "" || group.ProtocolFallbacks != nil) {
		ctx = WithClientProtocol(context.WithValue(ctx, ctxkey.Group, group), creativeOperationProtocol(platform, run.Operation))
	}
	groupID := run.GroupID
	var selection *AccountSelectionResult
	switch platform {
	case PlatformOpenAI:
		if e.gateway == nil {
			return nil, errors.New("creative OpenAI gateway is not configured")
		}
		selection, _, err = e.gateway.SelectAccountWithSchedulerForImages(
			ctx,
			&groupID,
			"",
			run.Model,
			nil,
			OpenAIImagesCapabilityNative,
		)
	case PlatformGrok:
		if e.gateway == nil {
			return nil, errors.New("creative OpenAI gateway is not configured")
		}
		selection, _, err = e.gateway.SelectAccountWithSchedulerForCapability(
			ctx,
			&groupID,
			"",
			"",
			run.Model,
			nil,
			OpenAIUpstreamTransportHTTPSSE,
			OpenAIEndpointCapabilityGrokMediaGeneration,
			false,
			false,
			PlatformGrok,
		)
	case PlatformGemini:
		if e.gatewayService == nil {
			return nil, errors.New("creative gateway service is not configured")
		}
		selection, err = e.gatewayService.SelectAccountWithLoadAwareness(ctx, &groupID, "", run.Model, nil, "", 0)
	default:
		return nil, creativeNonRetryableError("creative executor unsupported account platform %s", platform)
	}
	if err != nil {
		return nil, err
	}
	if selection == nil || selection.Account == nil {
		return nil, creativeNonRetryableError("no compatible creative account available for group %d model %s", run.GroupID, run.Model)
	}
	if !selection.Acquired {
		// 异步队列自身承担等待职责，不能阻塞 worker 等待账号槽位。
		if selection.WaitPlan != nil {
			return nil, ErrCreativeExecutionPending
		}
		return nil, creativeNonRetryableError("creative account %d was not admitted", selection.Account.ID)
	}
	upstreamModel := strings.TrimSpace(resolveAccountUpstreamModel(ctx, selection.Account, run.Model))
	if upstreamModel == "" {
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		return nil, creativeNonRetryableError("creative account %d has no upstream model for %s", selection.Account.ID, run.Model)
	}
	// 账号/渠道映射后必须再次校验最终模型能力，避免文本模型别名被错误送入图片执行器。
	if !creativePlatformImageModel(platform, upstreamModel) {
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		return nil, creativeNonRetryableError("creative mapped model %s is not an image model", upstreamModel)
	}
	return &CreativeExecution{
		Account:       selection.Account,
		UpstreamModel: upstreamModel,
		Selection:     selection,
		ReleaseFunc:   selection.ReleaseFunc,
	}, nil
}

func creativePlatformImageModel(platform, model string) bool {
	switch platform {
	case PlatformOpenAI:
		return IsGPTImageGenerationModel(model)
	case PlatformGrok:
		return isGrokImageGenerationModel(model)
	case PlatformGemini:
		return isCreativeGeminiImageModel(model)
	default:
		return false
	}
}

// Execute 使用 Prepare 返回的账号上下文调用上游。
func (e *CreativeExecutor) Execute(ctx context.Context, run CreativeRun, payload CreativeRunPayload, execution *CreativeExecution) (*CreativeExecuteResult, error) {
	if e == nil {
		return nil, errors.New("creative executor is not configured")
	}
	if execution == nil || execution.Account == nil {
		return nil, errors.New("creative execution context is not configured")
	}
	account := execution.Account
	// 创作台固定每次只生成一张，避免历史载荷或内部调用携带多图数量。
	run.RequestedOutputCount = 1
	upstreamModel := strings.TrimSpace(execution.UpstreamModel)
	if upstreamModel == "" {
		return nil, errors.New("creative execution upstream model is not configured")
	}
	execCtx, cancel := context.WithTimeout(ctx, e.executeTimeout())
	defer cancel()

	var outputs []CreativeOutput
	var err error
	switch account.Platform {
	case PlatformOpenAI:
		outputs, err = e.executeOpenAI(execCtx, run, payload, account, upstreamModel)
	case PlatformGrok:
		outputs, err = e.executeGrok(execCtx, run, payload, account, upstreamModel)
	case PlatformGemini:
		outputs, err = e.executeGemini(execCtx, run, payload, account, upstreamModel)
	default:
		return nil, creativeNonRetryableError("creative executor unsupported account platform %s", account.Platform)
	}
	if err != nil {
		e.reportScheduleResult(execution, account.ID, false)
		return nil, err
	}
	outputs, err = normalizeCreativeOutputs(outputs)
	if err != nil {
		e.reportScheduleResult(execution, account.ID, false)
		return nil, err
	}
	e.reportScheduleResult(execution, account.ID, true)
	return &CreativeExecuteResult{
		Outputs:   outputs,
		AccountID: account.ID,
	}, nil
}

// IsRetryable 实现 CreativeRunExecutor 接口。
func (e *CreativeExecutor) IsRetryable(err error) bool {
	return IsRetryableCreativeError(err)
}

// reportScheduleResult 将创作台执行结果反馈给实际使用的调度器。
func (e *CreativeExecutor) reportScheduleResult(execution *CreativeExecution, accountID int64, success bool) {
	if e == nil || execution == nil || execution.Selection == nil || accountID <= 0 {
		return
	}
	switch execution.Account.Platform {
	case PlatformOpenAI, PlatformGrok:
		if e.gateway != nil {
			e.gateway.ReportOpenAIAccountScheduleResultForSelection(execution.Selection, accountID, execution.UpstreamModel, success, nil)
		}
	case PlatformGemini:
		if e.gatewayService != nil {
			e.gatewayService.ReportAdvancedAccountScheduleResult(execution.Selection, accountID, success, nil)
		}
	}
}

// resolveGroupPlatform 读取分组平台；平台决定执行协议分派。
func (e *CreativeExecutor) resolveGroupPlatform(ctx context.Context, groupID int64) (string, error) {
	if e.groupRepo == nil {
		return "", errors.New("creative group repository is not configured")
	}
	group, err := e.groupRepo.GetByIDLite(ctx, groupID)
	if err != nil || group == nil {
		return "", creativeNonRetryableError("creative group %d is unavailable", groupID)
	}
	switch group.Platform {
	case PlatformOpenAI, PlatformGrok, PlatformGemini:
		return group.Platform, nil
	default:
		return "", creativeNonRetryableError("creative group platform %s is not supported", group.Platform)
	}
}

func (e *CreativeExecutor) executeTimeout() time.Duration {
	if e != nil && e.cfg != nil && e.cfg.Creative.ExecuteTimeoutSeconds > 0 {
		return time.Duration(e.cfg.Creative.ExecuteTimeoutSeconds) * time.Second
	}
	return defaultCreativeExecuteTimeout
}

// accountProxyURL 返回账号绑定的代理地址（无代理时为空串）。
func accountProxyURL(account *Account) string {
	if account == nil || account.ProxyID == nil || account.Proxy == nil {
		return ""
	}
	return account.Proxy.URL()
}

func readCreativeUpstreamBody(body io.Reader, limit int64) ([]byte, error) {
	return upstream.ReadLimitedBody(body, limit)
}

// normalizeCreativeOutputs 对执行器输出做后处理：
// 单张大小上限、固定取一张、sha256 去重。
func normalizeCreativeOutputs(outputs []CreativeOutput) ([]CreativeOutput, error) {
	if len(outputs) == 0 {
		return nil, creativeNonRetryableError("provider returned no image output")
	}
	seen := make(map[string]struct{}, len(outputs))
	out := make([]CreativeOutput, 0, len(outputs))
	for _, output := range outputs {
		if len(output.Bytes) == 0 {
			continue
		}
		if len(output.Bytes) > creativeMaxOutputBytes {
			return nil, creativeNonRetryableError("creative output %d exceeds size limit %d bytes", output.Index, creativeMaxOutputBytes)
		}
		sum := sha256Hex(output.Bytes)
		if _, ok := seen[sum]; ok {
			continue
		}
		seen[sum] = struct{}{}
		out = append(out, output)
	}
	if len(out) == 0 {
		return nil, creativeNonRetryableError("provider returned no usable image output")
	}
	if len(out) > 1 {
		out = out[:1]
	}
	for index := range out {
		out[index].Index = index
	}
	return out, nil
}

// creativeOpenAIImageSize 把创作台尺寸档位映射为 OpenAI images 协议支持的像素尺寸。
// 4K 档位遵守 GPT Image 2 的 3840 最大边长和约 8.3MP 总像素上限。
func creativeOpenAIImageSize(imageSize, aspectRatio string) string {
	tier := NormalizeImageBillingTierOrDefault(imageSize)
	ratio := strings.TrimSpace(aspectRatio)
	if tier == ImageBillingSize4K {
		switch ratio {
		case "16:9":
			return "3840x2160"
		case "9:16":
			return "2160x3840"
		case "4:3":
			return "3264x2448"
		case "3:4":
			return "2448x3264"
		default:
			return "2880x2880"
		}
	}
	base := 1024
	if tier != ImageBillingSize1K {
		base = 1536
	}
	switch ratio {
	case "16:9", "3:2", "4:3", "2:1", "19.5:9", "20:9":
		return fmt.Sprintf("%dx%d", base+base/2, base)
	case "9:16", "3:4", "2:3", "1:2", "9:19.5", "9:20":
		return fmt.Sprintf("%dx%d", base, base+base/2)
	default:
		return fmt.Sprintf("%dx%d", base, base)
	}
}

// creativeGrokImageResolution 把尺寸档位映射为 grok imagine 的 resolution（1k/2k）。
func creativeGrokImageResolution(imageSize string) string {
	if NormalizeImageBillingTierOrDefault(imageSize) == ImageBillingSize1K {
		return "1k"
	}
	return "2k"
}

func creativeGrokAspectRatio(aspectRatio string) string {
	return nativegrok.NormalizeImagineAspectRatio(aspectRatio)
}

type decodedCreativeImage = upstream.DecodedImage

func decodeBase64Image(raw string) (decodedCreativeImage, error) {
	return upstream.DecodeBase64Image(raw)
}

// creativeFileExtension 返回 MIME 对应的文件扩展名（用于 multipart 文件名）。
func creativeFileExtension(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

// BindNativeAttemptActivity 仅登记实际原生尝试，任务租约仍由所属 worker 持有。
func (e *CreativeExecutor) BindNativeAttemptActivity(enter func() (func(), error)) {
	e.nativeAttemptActivity = enter
}
