// 本文件把账号凭据规则与 Qoder 站点交换连接起来，不持有授权缓存。
package provider

import (
	"context"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/account"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

type qoderCredentialValidator struct {
	transport     QoderTransport
	profiles      *egressprovider.TLSProfiles
	validatePAT   func(context.Context, *account.Record, string, *qoder.MachineIdentity) (*qoder.AuthIdentity, error)
	validateCNPAT func(context.Context, *account.Record, string, *qoder.MachineIdentity, qoder.RequestDoer) (*qoder.AuthIdentity, error)
}

// CreateCredentialHooks 保留新建机器身份、编辑兼容和 PAT 校验的原有时机。
func CreateCredentialHooks(transport QoderTransport, profiles *egressprovider.TLSProfiles) account.CreateCredentialHooks {
	return (&qoderCredentialValidator{transport: transport, profiles: profiles}).hooks()
}

func (v *qoderCredentialValidator) hooks() account.CreateCredentialHooks {
	return account.CreateCredentialHooks{
		Site: func(value *account.Record) (string, error) {
			site, err := qoderSiteForRecord(value)
			return string(site), err
		},
		Prepare: func(value *account.Record) {
			value.Credentials = account.CloneValues(value.Credentials)
			ensureQoderMachineCredentials(value)
		},
		Validate:     v.validate,
		ValidateEdit: v.validateEdit,
	}
}

func (v *qoderCredentialValidator) validate(ctx context.Context, value *account.Record) error {
	return v.validateEdit(ctx, value, false)
}

func (v *qoderCredentialValidator) validateEdit(ctx context.Context, value *account.Record, deferPAT bool) error {
	if value != nil {
		value.Credentials = account.CloneValues(value.Credentials)
		value.Extra = account.CloneValues(value.Extra)
	}
	return account.ValidateQoderCredentials(ctx, value, deferPAT, account.QoderCredentialValidation{
		Normalize: func(site, mode string) (string, error) {
			parsed, err := qoder.ParseSite(site)
			if err != nil {
				return "", err
			}
			_, err = qoder.ParseRefreshMode(mode)
			return string(parsed), err
		},
		ValidatePAT: func(ctx context.Context, value *account.Record, site, pat string) error {
			machine := qoder.MachineForCredentials(QoderCredentialInput(value))
			doer := QoderRequestDoer(value, v.transport, v.profiles)
			if qoder.Site(site) == qoder.SiteCN {
				validate := v.validateCNPAT
				if validate == nil {
					validate = func(ctx context.Context, _ *account.Record, pat string, machine *qoder.MachineIdentity, doer qoder.RequestDoer) (*qoder.AuthIdentity, error) {
						profile, err := qoder.ProfileForSite(qoder.SiteCN)
						if err != nil {
							return nil, err
						}
						identity, _, err := qoder.ExchangeQoderCN20PATContext(ctx, pat, machine, profile, doer)
						return identity, err
					}
				}
				if _, err := validate(ctx, value, pat, machine, doer); err != nil {
					return fmt.Errorf("validate qoder cn pat: %w", err)
				}
				return nil
			}
			validate := v.validatePAT
			if validate == nil || v.transport != nil {
				validate = func(ctx context.Context, _ *account.Record, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
					return qoder.ExchangePATContext(ctx, pat, machine, "", doer)
				}
			}
			if _, err := validate(ctx, value, pat, machine); err != nil {
				return fmt.Errorf("validate qoder pat: %w", err)
			}
			return nil
		},
	})
}

func qoderSiteForRecord(value *account.Record) (qoder.Site, error) {
	if value == nil {
		return qoder.SiteGlobal, fmt.Errorf("qoder: account is nil")
	}
	return qoder.ParseSite(value.GetCredential("site"))
}
