package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
)

var qoderValidatePAT = func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return qoder.ExchangePAT(pat, machine, "")
}

func ValidateQoderCosyCredentials(ctx context.Context, account *Account) error {
	if account == nil || account.Platform != PlatformQoder || account.Type != AccountTypeCosy {
		return nil
	}
	if account.Credentials == nil {
		return errors.New("qoder cosy credentials are required")
	}

	pat := strings.TrimSpace(account.GetCredential("pat"))
	if pat != "" {
		if _, err := qoderValidatePAT(ctx, pat, qoder.NewMachine()); err != nil {
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
	if machineID != "" {
		return nil
	}

	return errors.New("qoder cosy credentials require pat, security_oauth_token+machine_id, or machine_id")
}
