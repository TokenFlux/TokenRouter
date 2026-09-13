// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

// BackgroundSkipAction 定义后台刷新服务在“未实际刷新”场景的计数方式。
type BackgroundSkipAction int

const (
	// BackgroundSkipAsSkipped 计入 skipped（保持当前默认行为）。
	BackgroundSkipAsSkipped BackgroundSkipAction = iota
	// BackgroundSkipAsSuccess 计入 success（仅用于兼容旧统计口径时可选）。
	BackgroundSkipAsSuccess
)

// BackgroundRefreshPolicy 描述后台刷新服务的调用侧策略。
type BackgroundRefreshPolicy struct {
	OnLockHeld       BackgroundSkipAction
	OnAlreadyRefresh BackgroundSkipAction
}

func DefaultBackgroundRefreshPolicy() BackgroundRefreshPolicy {
	return BackgroundRefreshPolicy{
		OnLockHeld:       BackgroundSkipAsSkipped,
		OnAlreadyRefresh: BackgroundSkipAsSkipped,
	}
}

func (p BackgroundRefreshPolicy) HandleLockHeld() error {
	if p.OnLockHeld == BackgroundSkipAsSuccess {
		return nil
	}
	return ErrRefreshSkipped
}

func (p BackgroundRefreshPolicy) HandleAlreadyRefreshed() error {
	if p.OnAlreadyRefresh == BackgroundSkipAsSuccess {
		return nil
	}
	return ErrRefreshSkipped
}
