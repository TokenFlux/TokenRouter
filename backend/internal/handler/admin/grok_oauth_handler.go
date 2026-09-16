// 旧 Grok HTTP 名称只转接账号 Adapter，原业务规则和并发导入已迁出。
package admin

import (
	"context"
	"log/slog"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

type GrokOAuthHandler = accounthttp.GrokOAuthHandler

func NewGrokOAuthHandler(auth *service.GrokOAuthService, admin service.AdminService, quota *service.GrokQuotaService, reconciler service.GrokOAuthReconciler, probers ...grokImportProber) *GrokOAuthHandler {
	return NewGrokOAuthHandlerWithImportProbes(newStandaloneImportProbes(), auth, admin, quota, reconciler, probers...)
}
func NewGrokOAuthHandlerWithImportProbes(queue *accountcore.GrokImportProbeScheduler, auth *service.GrokOAuthService, admin service.AdminService, quota *service.GrokQuotaService, reconciler service.GrokOAuthReconciler, probers ...grokImportProber) *GrokOAuthHandler {
	var authCore *accountcore.GrokAuthorization
	if auth != nil {
		authCore = auth.Core()
	}
	var quotaCore *accountcore.GrokQuotaService
	if quota != nil {
		quotaCore = quota.Core()
	}
	var prober grokImportProber = quota
	if len(probers) > 0 {
		prober = probers[0]
	}
	imports := accountcore.NewGrokAccountImport(authCore, accountcore.GrokAccountImportOptions{Get: func(ctx context.Context, id int64) (*accountcore.Record, error) {
		v, err := admin.GetAccount(ctx, id)
		return service.AccountRecordView(v), err
	}, Create: func(ctx context.Context, input *accountcore.CreateAccountInput) (*accountcore.Record, error) {
		v, err := admin.CreateAccount(ctx, input)
		return service.AccountRecordView(v), err
	}, Update: func(ctx context.Context, id int64, input *accountcore.UpdateAccountInput) (*accountcore.Record, error) {
		v, err := admin.UpdateAccount(ctx, id, input)
		return service.AccountRecordView(v), err
	}, NormalizeToken: xai.NormalizeSSOToken, LogError: slog.Error, RunTask: func(label string, work func()) { service.RunBackgroundTask(label, service.BackgroundCall0(work)) }, Schedule: func(v *accountcore.Record) {
		if prober == nil {
			return
		}
		snapshot := service.AccountSnapshotView(service.AccountFromRecord(v))
		queue.Schedule(legacyImportQuotaProbe{prober}, &snapshot)
	}})
	return accounthttp.NewGrokOAuthHandler(authCore, imports, quotaCore, accounthttp.GrokOAuthHTTPOptions{Reconciler: reconciler, RuntimeSanity: func() any { return xai.RuntimeSanity() }, ProxyURL: func(ctx context.Context, id int64) (string, bool, error) {
		v, err := admin.GetProxy(ctx, id)
		if err != nil || v == nil {
			return "", false, err
		}
		return v.URL(), true, nil
	}})
}

type GrokGenerateAuthURLRequest = accounthttp.GrokGenerateAuthURLRequest
type GrokExchangeCodeRequest = accounthttp.GrokExchangeCodeRequest
type GrokRefreshTokenRequest = accounthttp.GrokRefreshTokenRequest
type GrokSSOTokenRequest = accounthttp.GrokSSOTokenRequest
type GrokPasswordAuthorizeRequest = accounthttp.GrokPasswordAuthorizeRequest
type GrokOAuthReconcileRequest = accounthttp.GrokOAuthReconcileRequest
type GrokSSOToOAuthRequest = accounthttp.GrokSSOToOAuthRequest
type GrokSSOToOAuthItemResult = accounthttp.GrokSSOToOAuthItemResult
type GrokSSOToOAuthResponse = accounthttp.GrokSSOToOAuthResponse
