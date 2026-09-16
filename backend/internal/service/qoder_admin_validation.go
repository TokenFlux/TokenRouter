package service

import (
	"context"
	"fmt"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

var qoderValidatePAT = func(ctx context.Context, account *Account, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
	return qoder.ExchangePATContext(ctx, pat, machine, "", nil)
}

var qoderValidateCNPAT = func(ctx context.Context, account *Account, pat string, machine *qoder.MachineIdentity, doer qoder.RequestDoer) (*qoder.AuthIdentity, error) {
	profile, err := qoder.ProfileForSite(qoder.SiteCN)
	if err != nil {
		return nil, err
	}
	identity, _, err := qoder.ExchangeQoderCN20PATContext(ctx, pat, machine, profile, doer)
	return identity, err
}

func ValidateQoderCosyCredentials(ctx context.Context, account *Account) error {
	return validateQoderCosyCredentials(ctx, account, nil, nil)
}

func validateQoderCosyCredentials(ctx context.Context, account *Account, httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService) error {
	return validateQoderCosyCredentialsWithOptions(ctx, account, httpUpstream, tlsFPProfileService, false)
}

func validateQoderCosyCredentialsWithOptions(ctx context.Context, value *Account, httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService, deferPATExchange bool) error {
	return accountcore.ValidateQoderCredentials(ctx, AccountRecordView(value), deferPATExchange, accountcore.QoderCredentialValidation{
		Normalize: func(site, mode string) (string, error) {
			parsed, err := qoder.ParseSite(site)
			if err != nil {
				return "", err
			}
			_, err = qoder.ParseRefreshMode(mode)
			return string(parsed), err
		},
		ValidatePAT: func(ctx context.Context, _ *accountcore.Record, site, pat string) error {
			machine := qoderMachineForAccount(value)
			doer := newQoderRequestDoer(value, httpUpstream, tlsFPProfileService)
			if qoder.Site(site) == qoder.SiteCN {
				if _, err := qoderValidateCNPAT(ctx, value, pat, machine, doer); err != nil {
					return fmt.Errorf("validate qoder cn pat: %w", err)
				}
				return nil
			}
			validatePAT := qoderValidatePAT
			if httpUpstream != nil {
				validatePAT = func(ctx context.Context, account *Account, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
					return qoder.ExchangePATContext(ctx, pat, machine, "", newQoderRequestDoer(account, httpUpstream, tlsFPProfileService))
				}
			}
			if _, err := validatePAT(ctx, value, pat, machine); err != nil {
				return fmt.Errorf("validate qoder pat: %w", err)
			}
			return nil
		},
	})
}
