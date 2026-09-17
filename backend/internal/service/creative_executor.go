package service

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/creative"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

// 创作台执行器常量。
const (
	defaultCreativeExecuteTimeout = 5 * time.Minute
	defaultCreativeMaxAttempts    = 3
)

type CreativeUpstreamError = creative.CreativeUpstreamError

func creativeNonRetryableError(format string, args ...any) *CreativeUpstreamError {
	return creative.CreativeNonRetryableError(format, args...)
}

func creativeHTTPStatusError(statusCode int, message string) *CreativeUpstreamError {
	return creative.CreativeHTTPStatusError(statusCode, message)
}

func IsRetryableCreativeError(err error) bool { return creative.IsRetryableCreativeError(err) }

// CreativeExecutor 仅保留旧执行签名的投影，不持有具体 GatewayService。
type CreativeExecutor struct {
	nativeAttemptActivity func() (func(), error)
	timeout               time.Duration
	bindings              CreativeExecutionBindings
}

func NewCreativeExecutor(
	cfg *config.Config,
	accountRepo CreativeAccountRepository,
	groupRepo CreativeGroupRepository,
	gateway *OpenAIGatewayService,
	gatewayService *GatewayService,
	geminiTokens *GeminiTokenProvider,
	settingService *SettingService,
) *CreativeExecutor {
	timeout := defaultCreativeExecuteTimeout
	if cfg != nil && cfg.Creative.ExecuteTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.Creative.ExecuteTimeoutSeconds) * time.Second
	}
	e := &CreativeExecutor{timeout: timeout}
	e.bindings = legacyCreativeExecutionBindings(cfg, groupRepo, gateway, gatewayService, geminiTokens, e)
	return e
}

func (e *CreativeExecutor) Prepare(ctx context.Context, run CreativeRun) (*CreativeExecution, error) {
	if e == nil {
		return nil, errors.New("creative executor is not configured")
	}
	var selected *AccountSelectionResult
	core := e.nativeExecutor(&selected)
	execution, err := core.Prepare(ctx, run)
	if err != nil {
		return nil, err
	}
	return &CreativeExecution{Account: selected.Account, UpstreamModel: execution.UpstreamModel, Selection: selected, ReleaseFunc: execution.ReleaseFunc, Native: execution}, nil
}

func (e *CreativeExecutor) Execute(ctx context.Context, run CreativeRun, payload CreativeRunPayload, execution *CreativeExecution) (*CreativeExecuteResult, error) {
	if e == nil {
		return nil, errors.New("creative executor is not configured")
	}
	if execution == nil || execution.Account == nil {
		return nil, errors.New("creative execution context is not configured")
	}
	if execution.Native != nil {
		return execution.Native.Target.Execute(ctx, run, payload)
	}
	selected := e.selectionProjection(execution.Account, execution.Selection)
	return creative.NewExecutionTarget(selected, execution.UpstreamModel, e.executeTimeout()).Execute(ctx, run, payload)
}

// IsRetryable 实现 CreativeRunExecutor 接口。
func (e *CreativeExecutor) IsRetryable(err error) bool {
	return IsRetryableCreativeError(err)
}

func (e *CreativeExecutor) reportScheduleResult(execution *CreativeExecution, accountID int64, success bool) {
	if e != nil && e.bindings.Report != nil {
		e.bindings.Report(execution, accountID, success)
	}
}

func (e *CreativeExecutor) executeTimeout() time.Duration {
	if e != nil && e.timeout > 0 {
		return e.timeout
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

// BindNativeAttemptActivity 仅登记实际原生尝试，任务租约仍由所属 worker 持有。
func (e *CreativeExecutor) BindNativeAttemptActivity(enter func() (func(), error)) {
	e.nativeAttemptActivity = enter
}
