package httpapi

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

func (s *OpenAILiveExecutor) prepareLiveAttestation(ctx context.Context) (string, string, error) {
	if s == nil || s.Attestation == nil {
		return "", "", &session.LiveAttestationUnavailableError{
			Reason: "TokenRouter has no platform DeviceCheck provider",
		}
	}
	if s.AttestationCipher == nil {
		return "", "", &session.LiveAttestationUnavailableError{
			Reason: "JWT secret is required to protect the Sideband attestation",
		}
	}
	header, err := s.Attestation.Generate(ctx)
	if err != nil {
		return "", "", &session.LiveAttestationUnavailableError{Reason: err.Error()}
	}
	ciphertext, err := s.AttestationCipher.Encrypt(header)
	if err != nil {
		return "", "", &session.LiveAttestationUnavailableError{
			Reason: "failed to protect the generated DeviceCheck attestation",
		}
	}
	return header, ciphertext, nil
}

func (s *OpenAILiveExecutor) decryptLiveAttestation(record *session.LiveCallRecord) (string, error) {
	if record == nil || strings.TrimSpace(record.AttestationCiphertext) == "" || s.AttestationCipher == nil {
		return "", &session.LiveAttestationUnavailableError{
			Reason: "the Live call has no reusable DeviceCheck attestation",
		}
	}
	header, err := s.AttestationCipher.Decrypt(record.AttestationCiphertext)
	if err != nil {
		return "", &session.LiveAttestationUnavailableError{
			Reason: "the Live call DeviceCheck attestation cannot be decrypted on this instance",
		}
	}
	return header, nil
}
