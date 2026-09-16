package liveattestation

import (
	"context"
	"errors"
)

var (
	ErrUnsupportedPlatform = errors.New("live attestation requires TokenRouter to run on Apple Silicon macOS; other server platforms are not supported")
	ErrChatGPTAppMissing   = errors.New("live attestation requires the official ChatGPT app on the TokenRouter server")
)

// Provider 在发起 Live 请求前生成 ChatGPT DeviceCheck attestation。
type Provider interface {
	Check(ctx context.Context) error
	Generate(ctx context.Context) (string, error)
}
