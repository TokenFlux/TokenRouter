package service

import (
	"context"
	"strings"

	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

const liveAttestationHeader = nativeopenai.LiveAttestationHeader

func newLiveAttestationCipher(cfg *config.Config) SecretEncryptor {
	if cfg == nil || strings.TrimSpace(cfg.JWT.Secret) == "" {
		return nil
	}
	return nativeopenai.NewLiveAttestationCipher(cfg.JWT.Secret)
}

func (s *OpenAIGatewayService) prepareLiveAttestation(ctx context.Context) (string, string, error) {
	if s == nil || s.liveAttestation == nil {
		return "", "", &LiveAttestationUnavailableError{
			Reason: "TokenRouter has no platform DeviceCheck provider",
		}
	}
	if s.liveAttestationCipher == nil {
		return "", "", &LiveAttestationUnavailableError{
			Reason: "JWT secret is required to protect the Sideband attestation",
		}
	}
	header, err := s.liveAttestation.Generate(ctx)
	if err != nil {
		return "", "", &LiveAttestationUnavailableError{Reason: err.Error()}
	}
	ciphertext, err := s.liveAttestationCipher.Encrypt(header)
	if err != nil {
		return "", "", &LiveAttestationUnavailableError{
			Reason: "failed to protect the generated DeviceCheck attestation",
		}
	}
	return header, ciphertext, nil
}

func (s *OpenAIGatewayService) decryptLiveAttestation(record *LiveCallRecord) (string, error) {
	if record == nil || strings.TrimSpace(record.AttestationCiphertext) == "" || s.liveAttestationCipher == nil {
		return "", &LiveAttestationUnavailableError{
			Reason: "the Live call has no reusable DeviceCheck attestation",
		}
	}
	header, err := s.liveAttestationCipher.Decrypt(record.AttestationCiphertext)
	if err != nil {
		return "", &LiveAttestationUnavailableError{
			Reason: "the Live call DeviceCheck attestation cannot be decrypted on this instance",
		}
	}
	return header, nil
}
