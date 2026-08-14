package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/qoder"
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

func validateQoderCosyCredentialsWithOptions(
	ctx context.Context,
	account *Account,
	httpUpstream HTTPUpstream,
	tlsFPProfileService *TLSFingerprintProfileService,
	deferPATExchange bool,
) error {
	if account == nil {
		return nil
	}
	if account.Platform != PlatformQoder {
		if account.Type == AccountTypeCosy {
			return fmt.Errorf("%s account type requires %s platform", AccountTypeCosy, PlatformQoder)
		}
		return nil
	}
	if account.Type != AccountTypeCosy {
		return fmt.Errorf("qoder accounts require %s account type", AccountTypeCosy)
	}
	if account.Credentials == nil {
		return errors.New("qoder cosy credentials are required")
	}
	site, err := qoderSiteForAccount(account)
	if err != nil {
		return err
	}
	if _, err := qoderRefreshModeForAccount(account); err != nil {
		return err
	}

	pat := strings.TrimSpace(account.GetCredential("pat"))
	if pat != "" {
		// 编辑仅切换站点时先保存原凭据，兼容性由连接测试使用新站点协议验证。
		if deferPATExchange {
			return nil
		}
		machine := qoderMachineForAccount(account)
		doer := newQoderRequestDoer(account, httpUpstream, tlsFPProfileService)
		if site == qoder.SiteCN {
			if _, err := qoderValidateCNPAT(ctx, account, pat, machine, doer); err != nil {
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
		if _, err := validatePAT(ctx, account, pat, machine); err != nil {
			return fmt.Errorf("validate qoder pat: %w", err)
		}
		return nil
	}

	token := strings.TrimSpace(account.GetCredential("security_oauth_token"))
	machineID := strings.TrimSpace(account.GetCredential("machine_id"))
	if token != "" {
		if machineID == "" {
			return errors.New("qoder cosy credentials require machine_id with security_oauth_token")
		}
		if strings.TrimSpace(account.GetCredential("uid")) == "" && strings.TrimSpace(account.GetCredential("aid")) == "" {
			return errors.New("qoder cosy credentials require uid or aid with security_oauth_token")
		}
		return nil
	}
	return errors.New("qoder cosy credentials require pat or security_oauth_token+machine_id")
}
