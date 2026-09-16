package failover

const (
	// OAuth429MaxAccountAttempts 保留 OpenAI 同账号窗口耗尽后的账号总预算。
	OAuth429MaxAccountAttempts    = 3
	OAuth429StormMinSwitches      = 1
	FirstOutputTimeoutMaxSwitches = 1
)

// OAuth429State 只属于本请求，Grok 的后续尝试不能跨请求共享。
type OAuth429State struct{ grokFollowupPending bool }

// OAuth429Account 是调用方已确定的认证类别投影，不读取凭据。
type OAuth429Account struct{ OpenAI, Grok bool }

// StopOAuth429 保留 OpenAI 和 Grok 不同的后续预算及无状态兼容路径。
func StopOAuth429(account OAuth429Account, status, failedSwitches int, state *OAuth429State) bool {
	if failedSwitches < OAuth429StormMinSwitches {
		return false
	}
	if state != nil && state.grokFollowupPending {
		return true
	}
	if account.Grok {
		if state == nil {
			return status == 429 && failedSwitches >= 2
		}
		if status == 429 {
			state.grokFollowupPending = true
		}
		return false
	}
	if status != 429 || !account.OpenAI {
		return false
	}
	return failedSwitches >= OAuth429MaxAccountAttempts
}

// FirstOutputExhausted 只累计已有首输出恢复资格，不把普通错误计入该预算。
func FirstOutputExhausted(eligible bool, switches *int) bool {
	if !eligible || switches == nil {
		return false
	}
	if *switches >= FirstOutputTimeoutMaxSwitches {
		return true
	}
	*switches = *switches + 1
	return false
}
