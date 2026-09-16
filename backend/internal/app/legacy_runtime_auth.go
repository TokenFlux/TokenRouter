// 本文件登记旧认证与会话资源；业务迁移后删除对应绑定，S16 清零。
// 启动按停止依赖的逆序排列，纯预热在消费者启动前完成。
package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type authRuntimeReady struct{}

func provideAuthRuntime(
	apiKeyService *service.APIKeyService,
	errorPassthrough *service.ErrorPassthroughService,
	oauth *service.OAuthService,
	openaiOAuth *service.OpenAIOAuthService,
	geminiOAuth *service.GeminiOAuthService,
	antigravityOAuth *service.AntigravityOAuthService,
	qoderOAuth *service.QoderOAuthService,
	qoderTokens *service.QoderTokenProvider,
	grokOAuth *service.GrokOAuthService,
	tlsFingerprintProfile *service.TLSFingerprintProfileService,
	tlsFingerprintRouter *service.TLSFingerprintRouterService,
	manager *lifecycle.Manager,
) *authRuntimeReady {
	manager.Register(lifecycle.Hook{Name: "APIKeyService", StartOrder: 195, StopOrder: 805, Start: func(ctx context.Context) error {
		if apiKeyService != nil {
			apiKeyService.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if apiKeyService != nil {
			return apiKeyService.StopContext(ctx)
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "OAuthService", StartOrder: 190, StopOrder: 810, Start: func(ctx context.Context) error {
		if oauth != nil {
			oauth.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if oauth != nil {
			return oauth.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "OpenAIOAuthService", StartOrder: 190, StopOrder: 810, Start: func(ctx context.Context) error {
		if openaiOAuth != nil {
			openaiOAuth.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if openaiOAuth != nil {
			return openaiOAuth.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "GeminiOAuthService", StartOrder: 190, StopOrder: 810, Start: func(ctx context.Context) error {
		if geminiOAuth != nil {
			geminiOAuth.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if geminiOAuth != nil {
			return geminiOAuth.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "AntigravityOAuthService", StartOrder: 190, StopOrder: 810, Start: func(ctx context.Context) error {
		if antigravityOAuth != nil {
			antigravityOAuth.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if antigravityOAuth != nil {
			return antigravityOAuth.StopContext(ctx)
		}
		return nil
	}})
	// 凭据构建在生产者停止后取消并等待，先于完成队列与共享连接释放。
	manager.Register(lifecycle.Hook{Name: "QoderCredentialSessions", StopOrder: 30, Stop: qoderTokens.StopContext})
	manager.Register(lifecycle.Hook{Name: "QoderOAuthService", StartOrder: 190, StopOrder: 810, Start: func(ctx context.Context) error {
		if qoderOAuth != nil {
			qoderOAuth.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if qoderOAuth != nil {
			return qoderOAuth.StopContext(ctx)
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "GrokOAuthService", StartOrder: 190, StopOrder: 810, Start: func(ctx context.Context) error {
		if grokOAuth != nil {
			grokOAuth.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if grokOAuth != nil {
			return grokOAuth.StopContext(ctx)
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "TLSFingerprintProfileService", StartOrder: 190, StopOrder: 810, Start: func(ctx context.Context) error {
		if tlsFingerprintProfile != nil {
			tlsFingerprintProfile.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if tlsFingerprintProfile != nil {
			tlsFingerprintProfile.Stop()
		}
		return nil
	}})
	manager.Register(lifecycle.Hook{Name: "TLSFingerprintRouterService", StartOrder: 190, StopOrder: 810, Start: func(ctx context.Context) error {
		if tlsFingerprintRouter != nil {
			tlsFingerprintRouter.Start()
		}
		return nil
	}, Stop: func(ctx context.Context) error {
		if tlsFingerprintRouter != nil {
			tlsFingerprintRouter.Stop()
		}
		return nil
	}})

	manager.Register(lifecycle.Hook{Name: "ErrorPassthroughService", StartOrder: 190, StopOrder: 810,
		Start: errorPassthrough.StartContext,
		Stop:  errorPassthrough.StopContext})
	return &authRuntimeReady{}
}
