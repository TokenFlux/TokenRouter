// 发票信息规范化与必填校验保持原行为。
package payment

import (
	"net/mail"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func NilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func TrimBillingInfo(info *BillingInfo) *BillingInfo {
	if info == nil {
		return nil
	}
	trimmed := &BillingInfo{
		Name:      strings.TrimSpace(info.Name),
		Email:     strings.TrimSpace(info.Email),
		TaxIDType: strings.TrimSpace(info.TaxIDType),
		TaxID:     strings.TrimSpace(info.TaxID),
	}
	if info.Address != nil {
		addr := &BillingAddress{
			Country:    strings.ToUpper(strings.TrimSpace(info.Address.Country)),
			Line1:      strings.TrimSpace(info.Address.Line1),
			Line2:      strings.TrimSpace(info.Address.Line2),
			City:       strings.TrimSpace(info.Address.City),
			State:      strings.TrimSpace(info.Address.State),
			PostalCode: strings.TrimSpace(info.Address.PostalCode),
		}
		if addr.Country != "" || addr.Line1 != "" || addr.Line2 != "" || addr.City != "" || addr.State != "" || addr.PostalCode != "" {
			trimmed.Address = addr
		}
	}
	if trimmed.Name == "" && trimmed.Email == "" && trimmed.Address == nil && trimmed.TaxIDType == "" && trimmed.TaxID == "" {
		return nil
	}
	return trimmed
}
func BillingInfoSnapshot(info *BillingInfo) map[string]any {
	info = TrimBillingInfo(info)
	if info == nil {
		return nil
	}
	snapshot := map[string]any{}
	if info.Name != "" {
		snapshot["name"] = info.Name
	}
	if info.Email != "" {
		snapshot["email"] = info.Email
	}
	if info.TaxIDType != "" {
		snapshot["tax_id_type"] = info.TaxIDType
	}
	if info.TaxID != "" {
		snapshot["tax_id"] = info.TaxID
	}
	if info.Address != nil {
		address := map[string]any{}
		if info.Address.Country != "" {
			address["country"] = info.Address.Country
		}
		if info.Address.Line1 != "" {
			address["line1"] = info.Address.Line1
		}
		if info.Address.Line2 != "" {
			address["line2"] = info.Address.Line2
		}
		if info.Address.City != "" {
			address["city"] = info.Address.City
		}
		if info.Address.State != "" {
			address["state"] = info.Address.State
		}
		if info.Address.PostalCode != "" {
			address["postal_code"] = info.Address.PostalCode
		}
		if len(address) > 0 {
			snapshot["address"] = address
		}
	}
	return snapshot
}
func ValidateBillingInfo(info *BillingInfo, fallbackEmail string) error {
	info = TrimBillingInfo(info)
	if info == nil {
		return infraerrors.BadRequest("BILLING_INFO_REQUIRED", "billing info is required for Stripe invoice")
	}
	if strings.TrimSpace(info.Name) == "" {
		return infraerrors.BadRequest("BILLING_NAME_REQUIRED", "billing name is required")
	}
	email := strings.TrimSpace(info.Email)
	if email == "" {
		email = strings.TrimSpace(fallbackEmail)
	}
	if email == "" {
		return infraerrors.BadRequest("BILLING_EMAIL_REQUIRED", "billing email is required")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return infraerrors.BadRequest("BILLING_EMAIL_INVALID", "billing email is invalid")
	}
	if (strings.TrimSpace(info.TaxIDType) == "") != (strings.TrimSpace(info.TaxID) == "") {
		return infraerrors.BadRequest("BILLING_TAX_ID_INVALID", "billing tax id type and value must be provided together")
	}
	return nil
}
