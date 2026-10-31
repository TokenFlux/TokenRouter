package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// configuration.go separates administrator configuration from encrypted plugin
// secrets. Submitted ordinary fields replace the ordinary object, while omitted
// secret fields retain their stored values and empty strings clear them. A
// disabled installation may retain incomplete required fields; enabling always
// validates the complete configuration before publishing it to a process.
//
// Encryption uses the host's persistent key, and responses expose only whether
// each secret is set. Version migration carries values only between fields of
// the same secrecy class, preventing a schema change from exposing a previously
// encrypted value as ordinary configuration. Each transformation allocates fresh
// maps so a loaded release remains stable during a concurrent administrative read.

// SecretCipher encrypts plugin configuration using the application's shared key.
type SecretCipher interface {
	// Encrypt encodes plaintext with authentication and a fresh encryption nonce.
	Encrypt(string) (string, error)
	// Decrypt verifies ciphertext authentication before returning its original plaintext.
	Decrypt(string) (string, error)
}

// Configuration owns schema validation and the persisted secret representation.
// @project-doc docs/architecture/plugin_system.md#configuration_secrets
type Configuration struct {
	cipher SecretCipher // shared authenticated encryption implementation
	// persistent permits storing new nonempty secrets only when the configured key survives a host restart.
	persistent bool
}

// NewConfiguration binds the shared cipher and its durable-key availability flag.
func NewConfiguration(cipher SecretCipher, persistent bool) *Configuration {
	return &Configuration{cipher: cipher, persistent: persistent}
}

// Plain reconstructs and validates the complete configuration for process initialization.
// The returned map is independently allocated. Missing required values and
// unreadable ciphertext fail before any plaintext reaches a plugin process.
func (c *Configuration) Plain(snapshot *VersionSnapshot) (map[string]any, error) {
	values, err := c.values(snapshot)
	if err != nil {
		return nil, err
	}
	result, err := snapshot.Manifest.Config.Normalize(values)
	if err != nil {
		return nil, configError(err)
	}
	return result, nil
}

// values combines independently decoded secrets with the snapshot's ordinary settings.
// Ciphertext decoding errors retain their cause for diagnostics. Ordinary values
// are scalar schema values, so copying their map does not share mutable descendants.
func (c *Configuration) values(snapshot *VersionSnapshot) (map[string]any, error) {
	values := make(map[string]any)
	maps.Copy(values, snapshot.Config)
	if snapshot.SecretConfig == "" {
		return values, nil
	}
	if c.cipher == nil {
		return nil, ErrSecretKeyMissing
	}
	plain, err := c.cipher.Decrypt(snapshot.SecretConfig)
	if err != nil {
		return nil, ErrConfigInvalid.WithCause(err)
	}
	var secrets map[string]string
	if err := json.Unmarshal([]byte(plain), &secrets); err != nil {
		return nil, ErrConfigInvalid.WithCause(err)
	}
	for _, field := range snapshot.Manifest.Config.Fields {
		if field.Type == "secret" {
			if value, ok := secrets[field.Key]; ok {
				values[field.Key] = value
			}
		}
	}
	return values, nil
}

// Public returns ordinary settings and presence markers for declared secrets.
// It never returns ciphertext or plaintext secret values. Decryption must succeed
// so an unreadable stored secret is not misrepresented as an intentionally empty field.
func (c *Configuration) Public(snapshot *VersionSnapshot) (map[string]any, error) {
	values, err := c.values(snapshot)
	if err != nil {
		return nil, err
	}
	for _, field := range snapshot.Manifest.Config.Fields {
		if field.Type == "secret" {
			value, _ := values[field.Key].(string)
			values[field.Key] = map[string]bool{"set": value != ""}
		}
	}
	return values, nil
}

// Set validates replacement values and preserves any omitted existing secret.
// The caller owns the snapshot and persists it only after success. Incomplete
// required fields are accepted only when requireComplete is false, allowing
// administrators to save a disabled plugin before supplying every required field.
//
// Plaintext is separated before replacing either persisted container. New secret
// values require a durable encryption key, while an entirely cleared secret
// object stores an empty ciphertext string. A validation or encryption failure
// leaves the supplied snapshot unchanged, including its previous encrypted value.
func (c *Configuration) Set(snapshot *VersionSnapshot, submitted map[string]any, requireComplete bool) error {
	old, err := c.values(snapshot)
	if err != nil {
		return err
	}
	values := make(map[string]any)
	maps.Copy(values, submitted)
	for _, field := range snapshot.Manifest.Config.Fields {
		if field.Type == "secret" {
			if _, supplied := values[field.Key]; !supplied {
				if value, exists := old[field.Key]; exists {
					values[field.Key] = value
				}
			}
		}
	}
	schema := snapshot.Manifest.Config
	if !requireComplete {
		// Clearing only required flags keeps all type and constraint validation active while disabled installations await missing administrator input.
		schema.Fields = slices.Clone(schema.Fields)
		for i := range schema.Fields {
			schema.Fields[i].Required = false
		}
	}
	values, err = schema.Normalize(values)
	if err != nil {
		return configError(err)
	}
	secrets := make(map[string]string)
	for _, field := range schema.Fields {
		if field.Type == "secret" {
			if value, ok := values[field.Key].(string); ok && value != "" {
				secrets[field.Key] = value
			}
			delete(values, field.Key)
		}
	}
	var encrypted string
	if len(secrets) > 0 {
		if !c.persistent || c.cipher == nil {
			return ErrSecretKeyMissing
		}
		plain, err := json.Marshal(secrets)
		if err != nil {
			return err
		}
		encrypted, err = c.cipher.Encrypt(string(plain))
		if err != nil {
			return ErrConfigInvalid.WithCause(err)
		}
	}
	// Publish both representations only after normalization and encryption succeed, preventing a failed secret update from partially replacing ordinary fields.
	snapshot.Config = values
	snapshot.SecretConfig = encrypted
	return nil
}

// Migrate carries compatible configuration fields into a newly accepted version.
// Removed fields remain in the old snapshot for rollback. A field changing
// between secret and ordinary starts unset, so the new schema cannot disclose
// plaintext through an ordinary configuration response or inherit an unintended secret.
//
// The new schema supplies defaults for absent ordinary fields and applies all
// current constraints. Enabled updates require a complete result before saving
// installation intent; disabled updates may remain incomplete until configured.
// The target snapshot is changed only after its replacement passes validation.
func (c *Configuration) Migrate(old, target *VersionSnapshot, requireComplete bool) error {
	values, err := c.values(old)
	if err != nil {
		return err
	}
	oldFields := make(map[string]manifest.Field)
	for _, field := range old.Manifest.Config.Fields {
		oldFields[field.Key] = field
	}
	carried := make(map[string]any)
	for _, field := range target.Manifest.Config.Fields {
		previous, ok := oldFields[field.Key]
		if ok && (previous.Type == "secret") == (field.Type == "secret") {
			if value, exists := values[field.Key]; exists {
				carried[field.Key] = value
			}
		}
	}
	return c.Set(target, carried, requireComplete)
}

// configError exposes schema issue paths without including submitted field values.
// Validation messages describe constraints rather than plaintext inputs. Other
// failures retain only their diagnostic cause under the generic configuration error.
func configError(err error) error {
	if validation, ok := errors.AsType[*manifest.ValidationError](err); ok {
		data, _ := json.Marshal(validation.Issues)
		return ErrConfigInvalid.WithMetadata(map[string]string{"fields": string(bytes.TrimSpace(data))})
	}
	return ErrConfigInvalid.WithCause(err)
}
