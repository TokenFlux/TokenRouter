//go:build !darwin

package liveattestation

import "context"

type unsupportedProvider struct{}

// NewProvider 在非 macOS 平台返回显式不支持的证明提供器。
func NewProvider() Provider {
	return unsupportedProvider{}
}

func (unsupportedProvider) Check(context.Context) error {
	return ErrUnsupportedPlatform
}

func (unsupportedProvider) Generate(context.Context) (string, error) {
	return "", ErrUnsupportedPlatform
}
