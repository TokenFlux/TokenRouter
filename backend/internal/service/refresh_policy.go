package service

import acctcore "github.com/TokenFlux/TokenRouter/internal/account"

type ProviderRefreshErrorAction = acctcore.ProviderRefreshErrorAction

const ProviderRefreshErrorReturn = acctcore.ProviderRefreshErrorReturn
const ProviderRefreshErrorUseExistingToken = acctcore.ProviderRefreshErrorUseExistingToken

type ProviderLockHeldAction = acctcore.ProviderLockHeldAction

const ProviderLockHeldUseExistingToken = acctcore.ProviderLockHeldUseExistingToken
const ProviderLockHeldWaitForCache = acctcore.ProviderLockHeldWaitForCache

type ProviderRefreshPolicy = acctcore.ProviderRefreshPolicy

func ClaudeProviderRefreshPolicy() ProviderRefreshPolicy {
	return acctcore.ClaudeProviderRefreshPolicy()
}

func OpenAIProviderRefreshPolicy() ProviderRefreshPolicy {
	return acctcore.OpenAIProviderRefreshPolicy()
}

func GeminiProviderRefreshPolicy() ProviderRefreshPolicy {
	return acctcore.GeminiProviderRefreshPolicy()
}

func AntigravityProviderRefreshPolicy() ProviderRefreshPolicy {
	return acctcore.AntigravityProviderRefreshPolicy()
}

func GrokProviderRefreshPolicy() ProviderRefreshPolicy { return acctcore.GrokProviderRefreshPolicy() }

type BackgroundSkipAction = acctcore.BackgroundSkipAction

const BackgroundSkipAsSkipped = acctcore.BackgroundSkipAsSkipped
const BackgroundSkipAsSuccess = acctcore.BackgroundSkipAsSuccess

type BackgroundRefreshPolicy = acctcore.BackgroundRefreshPolicy

func DefaultBackgroundRefreshPolicy() BackgroundRefreshPolicy {
	return acctcore.DefaultBackgroundRefreshPolicy()
}
