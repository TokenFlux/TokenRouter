package httpapi

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

func activeCodexFingerprintMode(account *gatewayprovider.ExecutionAccount) accountcore.CodexFingerprintMode {
	if account == nil || gatewayprovider.ExecutionProtocolRecord(account).GetCodexFingerprintMode() == accountcore.CodexFingerprintOff {
		return accountcore.CodexFingerprintOff
	}
	if _, ok := accountcore.CodexFingerprintSeed(account.Record.Extra); !ok {
		return accountcore.CodexFingerprintOff
	}
	return gatewayprovider.ExecutionProtocolRecord(account).GetCodexFingerprintMode()
}
func openAIWSPoolAccountView(account *gatewayprovider.ExecutionAccount) *openai.WSPoolAccount {
	if account == nil {
		return nil
	}
	return &openai.WSPoolAccount{ID: account.Record.ID, Concurrency: account.Record.Concurrency, Type: account.Record.Type, FingerprintMode: string(activeCodexFingerprintMode(account))}
}
