// Executor 拥有单次任务尝试的准备、输出检查和反馈，不持有账号凭据或具体平台服务。
package creative

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ExecutionGroup 只提供当前分组的平台与本次调度所需的协议投影。
type ExecutionGroup struct {
	Platform         string
	ConfigureContext func(context.Context, string, string) context.Context
}
type Selection struct {
	AccountID         int64
	Platform          string
	Acquired, Waiting bool
	ResolveModel      func(context.Context, string) string
	Release           func()
	Execute           func(context.Context, CreativeRun, CreativeRunPayload, string) ([]CreativeOutput, error)
	Report            func(string, bool)
}
type Executor struct {
	Group                func(context.Context, int64) (*ExecutionGroup, error)
	OpenAI, Grok, Gemini func(context.Context, CreativeRun) (*Selection, error)
	Timeout              time.Duration
}

func (e *Executor) ResolveGroupPlatform(ctx context.Context, id int64) (string, error) {
	if e.Group == nil {
		return "", errors.New("creative group repository is not configured")
	}
	group, err := e.Group(ctx, id)
	if err != nil || group == nil {
		return "", CreativeNonRetryableError("creative group %d is unavailable", id)
	}
	switch group.Platform {
	case PlatformOpenAI, PlatformGrok, PlatformGemini:
		return group.Platform, nil
	default:
		return "", CreativeNonRetryableError("creative group platform %s is not supported", group.Platform)
	}
}
func (e *Executor) Prepare(ctx context.Context, run CreativeRun) (*CreativeExecution, error) {
	if e == nil {
		return nil, errors.New("creative executor is not configured")
	}
	platform, err := e.ResolveGroupPlatform(ctx, run.GroupID)
	if err != nil {
		return nil, err
	}
	// 保持原两次读取：第二次失败不改写已取得的平台，但不安装缺失的协议投影。
	if group, loadErr := e.Group(ctx, run.GroupID); loadErr == nil && group != nil && group.ConfigureContext != nil {
		ctx = group.ConfigureContext(ctx, platform, run.Operation)
	}
	var selectAccount func(context.Context, CreativeRun) (*Selection, error)
	switch platform {
	case PlatformOpenAI:
		selectAccount = e.OpenAI
	case PlatformGrok:
		selectAccount = e.Grok
	case PlatformGemini:
		selectAccount = e.Gemini
	default:
		return nil, CreativeNonRetryableError("creative executor unsupported account platform %s", platform)
	}
	if selectAccount == nil {
		if platform == PlatformGemini {
			return nil, errors.New("creative gateway service is not configured")
		}
		return nil, errors.New("creative OpenAI gateway is not configured")
	}
	selection, err := selectAccount(ctx, run)
	if err != nil {
		return nil, err
	}
	if selection == nil {
		return nil, CreativeNonRetryableError("no compatible creative account available for group %d model %s", run.GroupID, run.Model)
	}
	if !selection.Acquired {
		if selection.Waiting {
			return nil, ErrCreativeExecutionPending
		}
		return nil, CreativeNonRetryableError("creative account %d was not admitted", selection.AccountID)
	}
	model := strings.TrimSpace(selection.ResolveModel(ctx, run.Model))
	if model == "" {
		if selection.Release != nil {
			selection.Release()
		}
		return nil, CreativeNonRetryableError("creative account %d has no upstream model for %s", selection.AccountID, run.Model)
	}
	if !CreativePlatformImageModel(platform, model) {
		if selection.Release != nil {
			selection.Release()
		}
		return nil, CreativeNonRetryableError("creative mapped model %s is not an image model", model)
	}
	return &CreativeExecution{AccountID: selection.AccountID, UpstreamModel: model, ReleaseFunc: selection.Release, Target: NewExecutionTarget(selection, model, e.Timeout)}, nil
}
func (e *Executor) IsRetryable(err error) bool { return IsRetryableCreativeError(err) }

type preparedExecution struct {
	selection *Selection
	model     string
	timeout   time.Duration
}

// NewExecutionTarget 将本次账号、模型、反馈及预算固化，不再次选取账号。
func NewExecutionTarget(selection *Selection, model string, timeout time.Duration) ExecutionTarget {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &preparedExecution{selection: selection, model: model, timeout: timeout}
}
func (t *preparedExecution) Execute(ctx context.Context, run CreativeRun, payload CreativeRunPayload) (*CreativeExecuteResult, error) {
	if t == nil || t.selection == nil {
		return nil, errors.New("creative execution context is not configured")
	}
	model := strings.TrimSpace(t.model)
	if model == "" {
		return nil, errors.New("creative execution upstream model is not configured")
	}
	run.RequestedOutputCount = 1
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	s := t.selection
	switch s.Platform {
	case PlatformOpenAI, PlatformGrok, PlatformGemini:
	default:
		return nil, CreativeNonRetryableError("creative executor unsupported account platform %s", s.Platform)
	}
	outputs, err := s.Execute(ctx, run, payload, model)
	if err == nil {
		outputs, err = NormalizeCreativeOutputs(outputs)
	}
	if s.Report != nil {
		s.Report(model, err == nil)
	}
	if err != nil {
		return nil, err
	}
	return &CreativeExecuteResult{Outputs: outputs, AccountID: s.AccountID}, nil
}
