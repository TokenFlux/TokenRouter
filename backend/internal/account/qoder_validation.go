// 本文件拥有 Qoder 账号凭据形状和编辑校验，站点协议检查与交换通过端口注入。
package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type QoderCredentialValidation struct {
	Normalize   func(string, string) (string, error)
	ValidatePAT func(context.Context, *Record, string, string) error
}

func ValidateQoderCredentials(ctx context.Context, account *Record, deferPATExchange bool, ports QoderCredentialValidation) error {
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
	site, err := ports.Normalize(account.GetCredential("site"), account.GetCredential("refresh_mode"))
	if err != nil {
		return err
	}

	pat := strings.TrimSpace(account.GetCredential("pat"))
	if pat != "" {
		// 编辑仅切换站点时先保存原凭据，兼容性由连接测试使用新站点协议验证。
		if deferPATExchange {
			return nil
		}
		if err := ports.ValidatePAT(ctx, account, site, pat); err != nil {
			return err
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
