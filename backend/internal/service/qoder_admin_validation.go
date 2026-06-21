package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
)

var qoderValidatePAT = func(ctx context.Context, account *Account, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
	return qoder.ExchangePATContext(ctx, pat, machine, "", nil)
}

func ValidateQoderCosyCredentials(ctx context.Context, account *Account) error {
	return validateQoderCosyCredentials(ctx, account, nil, nil)
}

func validateQoderCosyCredentials(ctx context.Context, account *Account, httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService) error {
	if account == nil || account.Platform != PlatformQoder || account.Type != AccountTypeCosy {
		return nil
	}
	if account.Credentials == nil {
		return errors.New("qoder cosy credentials are required")
	}

	pat := strings.TrimSpace(account.GetCredential("pat"))
	if pat != "" {
		validatePAT := qoderValidatePAT
		if httpUpstream != nil {
			validatePAT = func(ctx context.Context, account *Account, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
				return qoder.ExchangePATContext(ctx, pat, machine, "", newQoderRequestDoer(account, httpUpstream, tlsFPProfileService))
			}
		}
		if _, err := validatePAT(ctx, account, pat, qoder.NewMachine()); err != nil {
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
