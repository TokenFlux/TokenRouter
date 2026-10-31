package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// email.go persists the terminal outcome of an already committed email claim.
// The conditional update changes state and result together, and accepts only a
// pending email row in the selected plugin namespace. Financial rows and prior
// terminal outcomes cannot be overwritten through this operation.

// FinalizeEmail records a completed, failed, or uncertain result for one pending claim.
// Callers retain the email key guard until this operation returns. A missing or
// changed claim fails, allowing the delivery coordinator to report uncertainty.
func (s *Store) FinalizeEmail(ctx context.Context, id, key string, state plugin.LedgerState, result json.RawMessage) error {
	if state != plugin.LedgerCompleted && state != plugin.LedgerFailed && state != plugin.LedgerUnknown {
		return fmt.Errorf("invalid terminal plugin email state")
	}
	updated, err := s.selectedClient(ctx).ExecContext(ctx, `UPDATE plugin_ledger SET state=$3,result=$4::jsonb,updated_at=NOW()
WHERE plugin_id=$1 AND idempotency_key=$2 AND kind='email_send' AND state='pending'`, id, key, string(state), string(result))
	if err != nil {
		return err
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("plugin email claim is missing or already finalized")
	}
	return nil
}
