package ws

import "context"

// ImagePolicy 是当前报文适用的图片资格投影，不持有账号/分组实体。
type ImagePolicy struct {
	Allowed  bool
	Explicit string
	Bridge   bool
}
type ImageBilling struct {
	Model     string
	SizeTier  string
	InputSize string
}

// RequestPort 每次只调用一个既有解析/策略能力，完整处理顺序由 Normalize 拥有。
type RequestPort interface {
	PromptReplace(context.Context, []byte) []byte
	Mutate([]byte, string, string) ([]byte, error)
	RequestedEffort([]byte, string) *string
	Reasoning([]byte, string) ([]byte, error)
	IsLite([]byte) bool
	Compatibility([]byte, bool) ([]byte, bool, error)
	AliasTools([]byte) ([]byte, error)
	Models(int, string, []byte) (string, string, error)
	ClassifyPrevious(string) string
	TurnMetadata() string
	ScopeIdentity([]byte) ([]byte, bool, error)
	NormalizeLite([]byte) ([]byte, error)
	ImagePolicy(context.Context, []byte) ImagePolicy
	BridgeImages([]byte) ([]byte, error)
	StripImages([]byte) ([]byte, bool, error)
	StripSparkImages([]byte, string) ([]byte, bool, error)
	ImageIntent(string, string, []byte) ([]byte, bool, bool)
	FeatureDenied()
	ImageDeniedMessage() string
	ImageBilling([]byte, string) (ImageBilling, error)
	FastPolicy(context.Context, int, string, []byte, bool) ([]byte, *PolicyBlocked, error)
	PolicyDenied()
	BlockedEvent(*PolicyBlocked) []byte
	WriteBlocked(context.Context, []byte)
	CloseError(int, string, error) error
	Log(string)
}
type NormalizeOptions struct {
	AccountID       int64
	OAuth           bool
	ForceHTTPBridge bool
}

// RequestNormalizer 只持有明确会话模型状态，构造不执行任何任务。
type RequestNormalizer struct {
	State   *IngressState
	Options NormalizeOptions
	Port    RequestPort
}
