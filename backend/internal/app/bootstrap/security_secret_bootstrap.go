package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/securitysecret"
	"github.com/TokenFlux/TokenRouter/internal/config"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// security_secret_bootstrap.go resolves persisted JWT and MCP OAuth signing
// secrets retained across backend restarts. OAuth always prepares its independent
// generated key so configuring the frontend URL can enable it at runtime.
// Multiple application instances need the same persisted JWT signing value
// even when their configured or generated candidates differ. Bootstrap trims
// the configured value, inserts a candidate only when its key is absent, then
// reads the stored value back as the result. An existing database value wins
// over a valid configured candidate, with mismatches logged before cfg is updated.
//
// Conflict-safe insertion and readback are separate operations. The read path
// retries only missing-row results for a bounded number of short waits,
// accommodating the absent-row outcome after an insert conflict. Stored values
// are checked by UTF-8 byte length after trimming, rather than by character
// count or claimed entropy. The generated-candidate flag is an equality
// comparison with readback, not a database insertion count.

// These values select the shared JWT record and bound missing-row readback
// retries after conflict-safe insertion: one initial read plus the extra attempts.
const (
	securitySecretKeyJWT        = "jwt_secret"          // JWT signing record key
	securitySecretReadRetryMax  = 5                     // extra missing-row attempts
	securitySecretReadRetryWait = 10 * time.Millisecond // missing-row retry interval
)

// readRandomBytes supplies secret generation's random reader and lets serial
// test cases inject failures before restoring the process-wide function.
var readRandomBytes = rand.Read

// ensureBootstrapSecrets resolves independent JWT and OAuth signing keys into
// cfg. Nil client or cfg returns an error, and cfg.JWT.Secret is trimmed
// before lookup or persistence, so a later failure can leave that trimmed
// value in cfg.
//
// A nonempty configured candidate is persisted only if the key is absent, then
// replaced with readback from the database. An empty candidate triggers
// generation through the same insertion/readback path. Stored values shorter
// than thirty-two bytes after trimming fail initialization. Configured
// mismatches and newly matching generated candidates are logged; successful
// resolution makes all callers use the readback value. A configured candidate
// below the byte minimum fails before insertion or readback, even when a valid
// value already exists in the database.
func ensureBootstrapSecrets(ctx context.Context, client *ent.Client, cfg *config.Config) error {
	if client == nil {
		return fmt.Errorf("nil ent client")
	}
	if cfg == nil {
		return fmt.Errorf("nil config")
	}

	// Resolve signing material even before a frontend URL is configured, so
	// enabling OAuth through site settings does not require restarting the service.
	value, _, err := getOrCreateGeneratedSecuritySecret(ctx, client, "mcp_oauth_secret", 32)
	if err != nil {
		return fmt.Errorf("ensure MCP OAuth secret: %w", err)
	}
	cfg.MCPOAuthSigningSecret = value
	cfg.Auth.JWT.Secret = strings.TrimSpace(cfg.Auth.JWT.Secret)
	if cfg.Auth.JWT.Secret != "" {
		storedSecret, err := createSecuritySecretIfAbsent(ctx, client, securitySecretKeyJWT, cfg.Auth.JWT.Secret)
		if err != nil {
			return fmt.Errorf("persist jwt secret: %w", err)
		}
		// An existing persisted value wins across instances. Report a mismatched
		// configured candidate before publishing the database readback into this config.
		if storedSecret != cfg.Auth.JWT.Secret {
			log.Println("Warning: configured JWT secret mismatches persisted value; using persisted secret for cross-instance consistency.")
		}
		cfg.Auth.JWT.Secret = storedSecret
		return nil
	}

	secret, created, err := getOrCreateGeneratedSecuritySecret(ctx, client, securitySecretKeyJWT, 32)
	if err != nil {
		return fmt.Errorf("ensure jwt secret: %w", err)
	}
	cfg.Auth.JWT.Secret = secret

	if created {
		log.Println("Warning: JWT secret auto-generated and persisted to database. Consider rotating to a managed secret for production.")
	}
	return nil
}

// getOrCreateGeneratedSecuritySecret returns an existing trimmed secret or
// generates a candidate for an absent key. Other initial query errors return
// directly, and stored values must contain at least thirty-two bytes after
// trimming.
//
// Generation occurs before conflict-safe insertion, so simultaneous callers
// can propose different candidates while returning the same database value.
// The boolean is true when readback equals this call's generated candidate; it
// is not a database insertion-ownership signal. A coinciding candidate can
// satisfy that comparison for more than one caller. byteLength selects
// generated raw bytes, while persistence checks the resulting string length.
func getOrCreateGeneratedSecuritySecret(ctx context.Context, client *ent.Client, key string, byteLength int) (string, bool, error) {
	existing, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(key)).Only(ctx)
	if err == nil {
		value := strings.TrimSpace(existing.Value)
		// Validate stored bytes after trimming because JWT configuration requires
		// a byte-length minimum, independent of the number of characters.
		if len([]byte(value)) < 32 {
			return "", false, fmt.Errorf("stored secret %q must be at least 32 bytes", key)
		}
		return value, false, nil
	}
	if !ent.IsNotFound(err) {
		return "", false, err
	}

	generated, err := generateHexSecret(byteLength)
	if err != nil {
		return "", false, err
	}

	value, err := persistSecuritySecretCandidate(ctx, client, key, generated)
	if err != nil {
		return "", false, err
	}
	return value, value == generated, nil
}

// createSecuritySecretIfAbsent trims and validates a configured candidate,
// then returns the persisted readback value. Candidates shorter than
// thirty-two bytes fail before insertion; an existing key keeps its database
// value even when this candidate differs.
func createSecuritySecretIfAbsent(ctx context.Context, client *ent.Client, key, value string) (string, error) {
	value = strings.TrimSpace(value)
	// Validate the trimmed candidate before persistence. The minimum is stored
	// string bytes and makes no statement about the candidate's random entropy.
	if len([]byte(value)) < 32 {
		return "", fmt.Errorf("secret %q must be at least 32 bytes", key)
	}

	return persistSecuritySecretCandidate(ctx, client, key, value)
}

// persistSecuritySecretCandidate inserts value with conflict handling on the
// secret key and returns the trimmed stored value. Insert errors other than
// recognized no-row results return directly; the subsequent read validates a
// thirty-two-byte minimum.
//
// DoNothing leaves an existing value authoritative when another creator has
// supplied the same key. The function therefore queries after the attempted
// insert instead of returning its local candidate. A compatible no-row
// execution error still permits that readback, and missing readback retries
// are bounded by querySecuritySecretWithRetry. Insert and read are separate
// operations under the caller's context.
func persistSecuritySecretCandidate(ctx context.Context, client *ent.Client, key, value string) (string, error) {
	// Keep an existing key unchanged when creators race. The following readback
	// determines the shared value even when this candidate is not inserted.
	if err := client.SecuritySecret.Create().
		SetKey(key).
		SetValue(value).
		OnConflictColumns(securitysecret.FieldKey).
		DoNothing().
		Exec(ctx); err != nil {
		// Conflict-compatible execution can surface a no-row result. Continue to
		// readback for that result while propagating other insertion failures immediately.
		if !isSQLNoRowsError(err) {
			return "", err
		}
	}

	stored, err := querySecuritySecretWithRetry(ctx, client, key)
	if err != nil {
		return "", err
	}
	storedValue := strings.TrimSpace(stored.Value)
	// Check persisted bytes too: a competing creator can supply the readback value.
	if len([]byte(storedValue)) < 32 {
		return "", fmt.Errorf("stored secret %q must be at least 32 bytes", key)
	}
	return storedValue, nil
}

// querySecuritySecretWithRetry reads one keyed row, allowing an initial
// attempt and five extra attempts for recognized missing-row errors. Other
// query errors return immediately. Each missing result waits ten milliseconds
// unless the attempt limit or ctx ends the wait.
//
// The loop retains the most recent missing error for exhaustion, while
// cancellation during a delay returns ctx.Err. A timer is stopped on that
// cancellation path so an abandoned wait does not retain its scheduled signal.
// Query execution also receives ctx, so the total duration includes query time
// in addition to the short retry delays.
func querySecuritySecretWithRetry(ctx context.Context, client *ent.Client, key string) (*ent.SecuritySecret, error) {
	var lastErr error
	for attempt := 0; attempt <= securitySecretReadRetryMax; attempt++ {
		stored, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(key)).Only(ctx)
		if err == nil {
			return stored, nil
		}
		if !isSecretNotFoundError(err) {
			return nil, err
		}
		lastErr = err
		if attempt == securitySecretReadRetryMax {
			break
		}

		// Missing readback is the retryable case after insertion. Delay the next
		// query within the caller's context instead of repeatedly querying without a wait.
		timer := time.NewTimer(securitySecretReadRetryWait)
		select {
		case <-ctx.Done():
			// This wait will not consume the scheduled timer event. Stop the timer
			// before returning the caller's cancellation result from the retry delay.
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

// isSecretNotFoundError recognizes Ent not-found and PostgreSQL no-row
// compatibility errors, returning false for nil.
func isSecretNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return ent.IsNotFound(err) || isSQLNoRowsError(err)
}

// isSQLNoRowsError delegates wrapped SQL and textual no-row recognition to
// PostgreSQL compatibility handling.
func isSQLNoRowsError(err error) bool { return postgresinfra.IsNoRows(err) }

// generateHexSecret encodes a requested raw-byte buffer as lowercase
// hexadecimal after calling the random reader. Nonpositive lengths select
// thirty-two bytes; reader errors are wrapped and returned before an encoded
// result is available.
func generateHexSecret(byteLength int) (string, error) {
	if byteLength <= 0 {
		byteLength = 32
	}
	buf := make([]byte, byteLength)
	if _, err := readRandomBytes(buf); err != nil {
		return "", fmt.Errorf("generate random secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
