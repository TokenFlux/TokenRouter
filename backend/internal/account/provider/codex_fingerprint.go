package provider

import (
	"time"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/google/uuid"
)

// ConvergedInstallationID 返回账号级恒定的 installation_id。
// 优先使用管理员配置的真实 device_id，无则从系统管理的账号随机种子确定性派生。
func ConvergedInstallationID(value *acctcore.Record, seed string) string {
	if value == nil {
		return ""
	}
	if deviceID := value.GetOpenAIDeviceID(); deviceID != "" {
		return deviceID
	}
	if seed == "" {
		return ""
	}
	return openai.DeriveStableUUIDv4("sub2api:codex-install-id:v2:" + seed)
}

func CodexFingerprintIDs(value *acctcore.Record, clientSessionID string, mode acctcore.CodexFingerprintMode) *openai.FingerprintIDs {
	if value == nil || mode == acctcore.CodexFingerprintOff {
		return nil
	}
	seed, ok := acctcore.CodexFingerprintSeed(value.Extra)
	if !ok {
		return nil
	}
	return openai.ResolveFingerprintIDs(value.ID, seed, clientSessionID, string(mode), func(seed string) string { return ConvergedInstallationID(value, seed) }, time.Now, func() string { return uuid.Must(uuid.NewV7()).String() })
}
