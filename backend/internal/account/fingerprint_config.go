// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	strings "strings"
)

// CodexFingerprintMode 控制 OAuth 账号出站请求的设备指纹收敛强度。
// 多人共享同一 OAuth 账号时，每个用户的 Codex 客户端会携带各自不同的
// installation_id / session_id / thread_id，上游据此判定设备数和会话数。
// 收敛模式将这些标识改写为账号级恒定值，减少上游可见的设备/会话指纹。
type CodexFingerprintMode string

const (
	// CodexFingerprintOff 不做任何收敛，原样透传客户端标识。
	// 这是默认值：收敛是显式 opt-in 的（见 GetCodexFingerprintMode）。
	CodexFingerprintOff CodexFingerprintMode = "off"
	// CodexFingerprintDevice 仅收敛 installation_id 为账号级恒定值。
	// 上游看到 1 台设备 + 多会话（每用户各自的 session）。
	CodexFingerprintDevice CodexFingerprintMode = "device"
	// CodexFingerprintSession 收敛 installation_id + session_id，
	// thread_id 按客户端原始 session-id 确定性派生（每个真实 Codex 会话一个独立线程）。
	// 上游看到 1 台设备 + 1 会话 + N 线程，最接近正常用户 spawn 子代理的模式。
	CodexFingerprintSession CodexFingerprintMode = "session"
	// CodexFingerprintFull 收敛所有标识：installation_id + session_id + thread_id。
	// 上游看到 1 台设备 + 1 会话 + 1 线程，最激进。
	CodexFingerprintFull CodexFingerprintMode = "full"
)

const (
	CodexFingerprintModeExtraKey = "codex_fingerprint_mode"
	CodexFingerprintSeedExtraKey = "codex_fingerprint_seed"
)

func CodexFingerprintModeFromExtra(extra map[string]any) CodexFingerprintMode {
	if extra == nil {
		return CodexFingerprintOff
	}
	raw, _ := extra[CodexFingerprintModeExtraKey].(string)
	switch CodexFingerprintMode(strings.TrimSpace(raw)) {
	case CodexFingerprintOff, CodexFingerprintDevice, CodexFingerprintSession, CodexFingerprintFull:
		return CodexFingerprintMode(strings.TrimSpace(raw))
	default:
		return CodexFingerprintOff
	}
}

func CodexFingerprintModeRequiresSeed(mode CodexFingerprintMode) bool {
	switch mode {
	case CodexFingerprintDevice, CodexFingerprintSession, CodexFingerprintFull:
		return true
	default:
		return false
	}
}

// ShouldEnsureCodexFingerprintSeedForExtraUpdates 判断 Extra 增量是否开启了
// Codex 指纹收敛；开启时仓储必须原子保留或生成系统管理的账号 seed。
func ShouldEnsureCodexFingerprintSeedForExtraUpdates(updates map[string]any) bool {
	if updates == nil {
		return false
	}
	return CodexFingerprintModeRequiresSeed(CodexFingerprintModeFromExtra(updates))
}

// GetCodexFingerprintMode 仅对 OAuth 类账号读取原有指纹模式，其他账号保持关闭。
func (a *Record) GetCodexFingerprintMode() CodexFingerprintMode {
	if a == nil || !a.IsOpenAIOAuthLike() {
		return CodexFingerprintOff
	}
	return CodexFingerprintModeFromExtra(a.Extra)
}
