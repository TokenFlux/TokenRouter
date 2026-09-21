package service

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

func newLiveAttestationCipher(cfg *config.Config) identity.SecretEncryptor {
	if cfg == nil || strings.TrimSpace(cfg.JWT.Secret) == "" {
		return nil
	}
	return openai.NewLiveAttestationCipher(cfg.JWT.Secret)
}

func (s *OpenAIGatewayService) prepareLiveAttestation(ctx context.Context) (string, string, error) {
	if s == nil || s.liveAttestation == nil {
		return "", "", &session.LiveAttestationUnavailableError{
			Reason: "TokenRouter has no platform DeviceCheck provider",
		}
	}
	if s.liveAttestationCipher == nil {
		return "", "", &session.LiveAttestationUnavailableError{
			Reason: "JWT secret is required to protect the Sideband attestation",
		}
	}
	header, err := s.liveAttestation.Generate(ctx)
	if err != nil {
		return "", "", &session.LiveAttestationUnavailableError{Reason: err.Error()}
	}
	ciphertext, err := s.liveAttestationCipher.Encrypt(header)
	if err != nil {
		return "", "", &session.LiveAttestationUnavailableError{
			Reason: "failed to protect the generated DeviceCheck attestation",
		}
	}
	return header, ciphertext, nil
}

func (s *OpenAIGatewayService) decryptLiveAttestation(record *session.LiveCallRecord) (string, error) {
	if record == nil || strings.TrimSpace(record.AttestationCiphertext) == "" || s.liveAttestationCipher == nil {
		return "", &session.LiveAttestationUnavailableError{
			Reason: "the Live call has no reusable DeviceCheck attestation",
		}
	}
	header, err := s.liveAttestationCipher.Decrypt(record.AttestationCiphertext)
	if err != nil {
		return "", &session.LiveAttestationUnavailableError{
			Reason: "the Live call DeviceCheck attestation cannot be decrypted on this instance",
		}
	}
	return header, nil
}
