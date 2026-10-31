package plugin

import (
	"context"
	"encoding/json"
	"time"
)

// writes.go commits normalized plugin intents and their results through one
// shared replay and quota workflow. The transaction first checks for an existing
// plugin-wide key, allowing a completed result to replay even when an administrator
// has since lowered the capability's limits. A reused key with different intent
// fails before any quota check or business effect can run.
//
// New operations read current installation policy after acquiring the database
// advisory lock. Their accounting timestamp is captured at that same point and
// passed to quota checks and ledger insertion, keeping waits across midnight
// within one consistent host-calendar day. Capability-specific callbacks own
// their quota dimensions and business operation; this engine owns transaction
// completion and never starts post-commit cache, history, or SMTP work itself.

// Writes owns the common transaction and replay workflow for plugin write capabilities.
type Writes struct {
	store    LedgerStore      // authoritative transaction and ledger storage
	policies PolicyReader     // current installation-level capability policy
	now      func() time.Time // accounting observation clock after locking
}

// Mutation contains immutable business intent prepared by one validated capability call.
type Mutation struct {
	Key     string          // plugin-wide operation replay key
	Kind    LedgerKind      // requested capability operation kind
	UserID  *int64          // optional target account identifier
	Amount  string          // exact nonnegative quota amount
	Request json.RawMessage // normalized intent excluding temporary grants
	State   LedgerState     // initial durable operation state
	Reason  string          // validated user-visible operation explanation
}

// QuotaCheck validates capability-specific spending under the locked plugin transaction.
type QuotaCheck func(context.Context, LedgerTransaction, Limits, time.Time) error

// BusinessEffect performs one transaction-bound operation and returns its durable result JSON.
type BusinessEffect func(context.Context) (json.RawMessage, error)

// WriteResult returns authoritative ledger state and whether the caller reused it.
type WriteResult struct {
	Entry    *LedgerEntry // committed or previously recorded operation
	Replayed bool         // existing intent reused its durable result
}

// NewWrites constructs the shared replay engine with an injectable accounting clock.
// A nil clock uses wall time. The policy source must read the same installation
// namespace used by the authenticated process binding and its ledger store.
func NewWrites(store LedgerStore, policies PolicyReader, now func() time.Time) *Writes {
	if now == nil {
		now = time.Now
	}
	return &Writes{store: store, policies: policies, now: now}
}

// Apply commits one validated capability operation or returns its original recorded result.
// Callers check permission, arguments, and required caller grants before entering.
// The normalized request must exclude grants and include every business input
// affecting the result. Quota and effect callbacks use the supplied transaction context.
//
// Replay lookup precedes current limits so lowering a policy cannot erase proof
// of an already committed operation. New work captures one accounting instant
// after the advisory lock, checks policy, runs its effect, and inserts the result
// within that transaction. Only a successful commit returns a nonnil result;
// callers can then perform auxiliary effects without repeating the business mutation.
// @project-doc docs/architecture/plugin_system.md#write_capabilities
func (w *Writes) Apply(ctx context.Context, binding Binding, mutation Mutation, quota QuotaCheck, effect BusinessEffect) (*WriteResult, error) {
	var result *WriteResult
	err := w.store.Transact(ctx, binding.PluginID, func(bound context.Context, tx LedgerTransaction) error {
		previous, err := tx.Find(bound, mutation.Key)
		if err != nil {
			return err
		}
		if previous != nil {
			if previous.Kind != mutation.Kind || !sameIntent(previous.Request, mutation.Request) {
				return ErrInvalidArgument
			}
			result = &WriteResult{Entry: previous, Replayed: true}
			return nil
		}
		installation, err := w.policies.Get(bound, binding.PluginID)
		if err != nil {
			return err
		}
		limits, err := ParseLimits(installation.Limits)
		if err != nil {
			return err
		}
		now := w.now()
		if err := quota(bound, tx, limits, now); err != nil {
			return err
		}
		payload, err := effect(bound)
		if err != nil {
			return err
		}
		entry := &LedgerEntry{
			PluginID:  binding.PluginID,
			Key:       mutation.Key,
			Kind:      mutation.Kind,
			UserID:    mutation.UserID,
			Amount:    mutation.Amount,
			Request:   mutation.Request,
			Result:    payload,
			State:     mutation.State,
			Reason:    mutation.Reason,
			CreatedAt: now,
		}
		if err := tx.Insert(bound, entry); err != nil {
			return err
		}
		result = &WriteResult{Entry: entry}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
