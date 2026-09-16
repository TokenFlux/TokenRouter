package admission

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// SelectionProblemKind 区分持久配置缺失和暂时容量问题，不决定 HTTP 状态码。
type SelectionProblemKind uint8

const (
	SelectionUnavailable SelectionProblemKind = iota
	SelectionModelNotFound
	SelectionRateLimited
)

// SelectionProblem 只保存对外安全消息；持久模型诊断不得泄露内部模型别名。
type SelectionProblem struct {
	Kind    SelectionProblemKind
	Message string
}

var selectionModelRateLimitedPattern = regexp.MustCompile(`(?:model_rate_limited|rate_limited)=(\d+)`)

// DiagnoseSelection 保留原查询条件和时机，空模型/分组不会新增存储读取。
func DiagnoseSelection(ctx context.Context, diag routing.ModelAvailabilityDiagnoser, groupID *int64, routingModel, displayModel, platform string) SelectionProblem {
	fallback := SelectionProblem{Kind: SelectionUnavailable, Message: "Service temporarily unavailable"}
	routingModel = strings.TrimSpace(routingModel)
	displayModel = strings.TrimSpace(displayModel)
	if displayModel == "" {
		displayModel = routingModel
	}
	if diag == nil || groupID == nil || routingModel == "" {
		return fallback
	}
	result := diag.DiagnoseModelAvailabilityForPlatform(ctx, groupID, routingModel, platform)
	if result.HasAccountsInPool && !result.HasModelSupport {
		return SelectionProblem{Kind: SelectionModelNotFound, Message: fmt.Sprintf("Model %q is not supported by any configured account in this group", displayModel)}
	}
	return fallback
}

// RefineSelectionFailure 保留模型配置诊断优先级，再解释原调度器的暂时限流计数。
func RefineSelectionFailure(err error, fallback SelectionProblem) SelectionProblem {
	if err == nil || fallback.Kind == SelectionModelNotFound {
		return fallback
	}
	match := selectionModelRateLimitedPattern.FindStringSubmatch(strings.ToLower(err.Error()))
	if len(match) != 2 {
		return fallback
	}
	count, parseErr := strconv.Atoi(match[1])
	if parseErr != nil || count <= 0 {
		return fallback
	}
	return SelectionProblem{Kind: SelectionRateLimited, Message: "All available accounts are currently rate-limited. Please retry later."}
}
