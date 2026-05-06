package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
)

func TestPSValidateBillingInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		info         *payment.BillingInfo
		fallbackMail string
		wantReason   string
	}{
		{
			name:         "requires billing info",
			info:         nil,
			fallbackMail: "buyer@example.com",
			wantReason:   "BILLING_INFO_REQUIRED",
		},
		{
			name: "requires name",
			info: &payment.BillingInfo{
				Email: "buyer@example.com",
			},
			wantReason: "BILLING_NAME_REQUIRED",
		},
		{
			name: "requires email when fallback is empty",
			info: &payment.BillingInfo{
				Name: "Buyer",
			},
			wantReason: "BILLING_EMAIL_REQUIRED",
		},
		{
			name: "rejects invalid email",
			info: &payment.BillingInfo{
				Name:  "Buyer",
				Email: "not-an-email",
			},
			wantReason: "BILLING_EMAIL_INVALID",
		},
		{
			name: "rejects tax id without type",
			info: &payment.BillingInfo{
				Name:  "Buyer",
				Email: "buyer@example.com",
				TaxID: "123456",
			},
			wantReason: "BILLING_TAX_ID_INVALID",
		},
		{
			name: "accepts fallback email",
			info: &payment.BillingInfo{
				Name: "Buyer",
			},
			fallbackMail: "buyer@example.com",
		},
		{
			name: "accepts tax id pair",
			info: &payment.BillingInfo{
				Name:      "Buyer",
				Email:     "buyer@example.com",
				TaxIDType: "eu_vat",
				TaxID:     "DE123456789",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := psValidateBillingInfo(tt.info, tt.fallbackMail)
			if tt.wantReason == "" {
				if err != nil {
					t.Fatalf("validate billing info: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected %s error, got nil", tt.wantReason)
			}
			appErr := infraerrors.FromError(err)
			if appErr.Reason != tt.wantReason {
				t.Fatalf("reason = %q, want %q", appErr.Reason, tt.wantReason)
			}
		})
	}
}

func TestPSBillingInfoSnapshotTrimsAndOmitsEmptyValues(t *testing.T) {
	t.Parallel()

	snapshot := psBillingInfoSnapshot(&payment.BillingInfo{
		Name:      "  Buyer Inc  ",
		Email:     "  billing@example.com  ",
		TaxIDType: "  eu_vat  ",
		TaxID:     "  DE123456789  ",
		Address: &payment.BillingAddress{
			Country:    " de ",
			Line1:      "  Main Street 1  ",
			Line2:      "  ",
			City:       "  Berlin  ",
			State:      "  BE  ",
			PostalCode: " 10115 ",
		},
	})

	if snapshot["name"] != "Buyer Inc" {
		t.Fatalf("name = %#v", snapshot["name"])
	}
	if snapshot["email"] != "billing@example.com" {
		t.Fatalf("email = %#v", snapshot["email"])
	}
	if snapshot["tax_id_type"] != "eu_vat" {
		t.Fatalf("tax_id_type = %#v", snapshot["tax_id_type"])
	}
	if snapshot["tax_id"] != "DE123456789" {
		t.Fatalf("tax_id = %#v", snapshot["tax_id"])
	}
	address, ok := snapshot["address"].(map[string]any)
	if !ok {
		t.Fatalf("address type = %T, want map[string]any", snapshot["address"])
	}
	if address["country"] != "DE" {
		t.Fatalf("country = %#v", address["country"])
	}
	if address["line1"] != "Main Street 1" {
		t.Fatalf("line1 = %#v", address["line1"])
	}
	if _, ok := address["line2"]; ok {
		t.Fatalf("line2 should be omitted: %#v", address)
	}
	if address["city"] != "Berlin" {
		t.Fatalf("city = %#v", address["city"])
	}
	if address["state"] != "BE" {
		t.Fatalf("state = %#v", address["state"])
	}
	if address["postal_code"] != "10115" {
		t.Fatalf("postal_code = %#v", address["postal_code"])
	}
}

func TestPSBillingInfoSnapshotReturnsNilForEmptyInfo(t *testing.T) {
	t.Parallel()

	if got := psBillingInfoSnapshot(&payment.BillingInfo{
		Name:    " ",
		Address: &payment.BillingAddress{Line1: " "},
	}); got != nil {
		t.Fatalf("snapshot = %#v, want nil", got)
	}
	if got := psBillingInfoSnapshot(nil); got != nil {
		t.Fatalf("snapshot = %#v, want nil", got)
	}
}
