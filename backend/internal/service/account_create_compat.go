// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func LegacyCreateCredentialHooks(upstream HTTPUpstream, tls *TLSFingerprintProfileService) acctcore.CreateCredentialHooks {
	return acctcore.CreateCredentialHooks{
		Site: func(value *acctcore.Record) (string, error) {
			site, err := qoderSiteForAccount(AccountFromRecord(value))
			return string(site), err
		},
		ValidateEdit: func(ctx context.Context, value *acctcore.Record, deferPAT bool) error {
			v := AccountFromRecord(value)
			err := validateQoderCosyCredentialsWithOptions(ctx, v, upstream, tls, deferPAT)
			value.Credentials = acctcore.CloneValues(v.Credentials)
			value.Extra = acctcore.CloneValues(v.Extra)
			return err
		},
		Prepare: func(value *acctcore.Record) {
			v := AccountFromRecord(value)
			ensureQoderMachineCredentials(v)
			value.Credentials = acctcore.CloneValues(v.Credentials)
		},
		Validate: func(ctx context.Context, value *acctcore.Record) error {
			v := AccountFromRecord(value)
			err := validateQoderCosyCredentials(ctx, v, upstream, tls)
			value.Credentials = acctcore.CloneValues(v.Credentials)
			value.Extra = acctcore.CloneValues(v.Extra)
			return err
		},
	}
}
