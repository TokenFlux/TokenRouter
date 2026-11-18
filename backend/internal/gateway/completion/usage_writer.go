package completion

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usage_writer.go persists usage records and supplements request summaries with
// completion evidence. Background usage contributes model and financial aliases
// to a request summary that the entrypoint may already have finished. Matching
// capture updates therefore leave its timing and terminal state intact. A
// different task ID instead receives a related completion record through the
// bound observer, keeping polling traffic separate from the task whose measured
// work is being logged. Recorded parent-child links let task writes reuse the
// parent's identifier observations and keep truncated usage values out of aliases.
//
// Usage persistence follows the funds attempt. Best-effort rejection triggers one
// synchronous Create call, using a fresh detached context when the earlier budget
// has expired. These writes keep the same analysis identity and never invoke
// settlement again. Missing logs skip both persistence and summary supplementation;
// configured write failures are reported through the recorder observer.

// WriteUsage supplements request metadata and attempts analysis persistence.
// Missing Logs or row returns immediately. ClientModel can override the row's
// requested display name, and matching request capture receives provider/model
// updates and aliases while keeping entrypoint timing and outcome.
//
// A nonmatching ID produces a completion record through RequestRecords. Storage
// runs under detached fifteen-second contexts; best-effort errors cause a single
// synchronous retry, with a renewed context when that budget has expired. Callers
// own the row during these updates and use SnapshotLogWriter when repositories
// may mutate their arguments. Write errors are observed and do not resubmit funds.
// Known diagnostic header observations suppress usage-derived aliases for the
// captured request and its explicitly linked children; unrelated tasks retain
// their existing sparse-record supplementation.
func (s *Recorder) WriteUsage(ctx context.Context, usageLog *UsageLog, logKey string) {
	writeStarted := time.Now()
	if s.logs == nil || usageLog == nil {
		observeUsageWrite(ctx, "skipped", "usage writer or row unavailable", nil)
		return
	}
	var finalError error
	mayHaveWritten := false
	acknowledged := false
	defer func() {
		state, message := "persisted", ""
		if !acknowledged && finalError == nil {
			state, message = "unknown", "usage write exited before acknowledgement"
		}
		if finalError != nil {
			state, message = "unknown", finalError.Error()
			if !mayHaveWritten && usage.IsUsageLogCreateNotPersisted(finalError) {
				state = "not_persisted"
			}
		}
		observeUsageWrite(ctx, state, message, usageLog.Timings, time.Since(writeStarted))
	}()
	applyClientModel(ctx, usageLog)
	if s.requestRecords != nil {
		now := time.Now().UTC()
		record := telemetry.RequestRecord{RequestID: usageLog.RequestID, StartedAt: usageLog.CreatedAt, UpdatedAt: now, FinishedAt: &now, State: "completed", UserID: usageLog.UserID, APIKeyID: usageLog.APIKeyID, ProviderID: usageLog.ProviderID, Model: usageLog.RequestedModel, Platform: usageLog.Platform}
		if record.StartedAt.IsZero() {
			record.StartedAt = now
		}
		if usageLog.TeamID != nil {
			record.TeamID = *usageLog.TeamID
		}
		if usageLog.InboundEndpoint != nil {
			record.Path = *usageLog.InboundEndpoint
		}
		if usageLog.BillingKey != "" {
			record.Aliases = append(record.Aliases, telemetry.RequestAlias{Kind: "billing", Value: usageLog.BillingKey})
		}
		// A matched capture may already be failed or cancelled; supplement its model
		// and aliases while retaining the entrypoint's terminal state, timing, and status.
		identifiersObserved := false
		matched := false
		telemetry.UpdateRequest(ctx, func(current *telemetry.RequestRecord) {
			matched = current.RequestID == record.RequestID
			related := false
			for _, alias := range current.Aliases {
				if alias.Kind == "related" && alias.Value == record.RequestID {
					related = true
				}
				if alias.Kind == "upstream_request" && alias.ProviderID == usageLog.ProviderID {
					identifiersObserved = true
				}
			}
			for _, attempt := range current.Attempts {
				if attempt.ProviderID == usageLog.ProviderID && attempt.RequestIDHeader != "" {
					identifiersObserved = true
				}
			}
			// A video task has a different logical ID but an explicit parent link.
			// Its already captured headers remain authoritative over the truncated usage value.
			identifiersObserved = identifiersObserved && (matched || related)
			if !matched {
				return
			}
			if record.Model != "" {
				current.Model = record.Model
			}
			if record.ProviderID > 0 {
				current.ProviderID = record.ProviderID
			}
			if record.Platform != "" {
				current.Platform = record.Platform
			}
		})
		if usageLog.UpstreamRequestID != nil && !identifiersObserved {
			record.Aliases = append(record.Aliases, telemetry.RequestAlias{Kind: "upstream", Value: *usageLog.UpstreamRequestID})
		}
		if matched {
			for _, alias := range record.Aliases {
				telemetry.AddRequestAlias(ctx, alias.Kind, alias.Value)
			}
		} else {
			// A task or otherwise unmatched ID needs its own summary; submitting this
			// record leaves the polling request's lifecycle and financial aliases unchanged.
			ctx = telemetry.WithRequestCapture(context.WithValue(ctx, telemetry.RequestID, record.RequestID), record, s.requestRecords)
		}
	}
	usageCtx, cancel := detachedBillingContext(ctx)
	defer cancel()

	if writer, ok := s.logs.(BestEffortLogWriter); ok {
		if err := writer.CreateBestEffort(usageCtx, usageLog); err != nil {
			mayHaveWritten = !usage.IsUsageLogCreateNotPersisted(err)
			s.printf(logKey, "Create usage log failed: %v", err)
			// Rejected analysis writes retry from the same row after the funds outcome
			// is already known; this path performs persistence alone and can also fail.
			fallbackCtx := usageCtx
			if usageCtx.Err() != nil {
				// An expired best-effort budget would reject the retry immediately; a fresh
				// detached timeout lets synchronous persistence make one additional attempt.
				var fallbackCancel context.CancelFunc
				fallbackCtx, fallbackCancel = detachedBillingContext(context.Background())
				defer fallbackCancel()
			}
			if _, syncErr := s.logs.Create(fallbackCtx, usageLog); syncErr != nil {
				finalError = syncErr
				s.printf(logKey, "Create usage log sync fallback failed: %v", syncErr)
			} else {
				acknowledged = true
			}
		} else {
			acknowledged = true
		}
		return
	}

	// A synchronous-only repository has no queue recovery path; report its failure
	// while leaving the earlier funds outcome and captured analysis identity intact.
	if _, err := s.logs.Create(usageCtx, usageLog); err != nil {
		finalError = err
		s.printf(logKey, "Create usage log failed: %v", err)
	} else {
		acknowledged = true
	}
}

// observeUsageWrite records persistence evidence without copying usage performance.
// Pending describes submission, while persisted requires a database acknowledgement.
// Unknown retains timeout evidence even if a queued write later becomes visible;
// request detail resolves that row through its ordinary indexed usage query.
//
// Only rejected or unsuccessful persistence keeps the bounded stage subset needed
// to locate the failure. Content intervals and TTFT policy stay with the queued
// usage payload. The shared request capture serializes this result with handler
// completion, preserving the request's own outcome and any earlier failed attempts.
func observeUsageWrite(ctx context.Context, state, message string, p *timing.Performance, elapsed ...time.Duration) {
	if p == nil {
		p = timing.PerformanceFromContext(ctx)
	}
	message = logredact.RedactText(message)
	if len(message) > 512 {
		message = message[:512]
	}
	telemetry.UpdateRequest(ctx, func(record *telemetry.RequestRecord) {
		record.UsageWriteUpdatedAt = time.Now().UTC()
		if len(elapsed) > 0 {
			ms := elapsed[0].Milliseconds()
			record.UsageWriteDurationMs = &ms
		}
		record.UsageWriteState = state
		record.UsageWriteError = message
		if p != nil {
			record.UsageAttempt = p.Values["attempt_number"]
			if state != "pending" && state != "persisted" {
				record.FailureTimings = p.FailureTimings()
			}
		}

		for i := range record.Attempts {
			if int64(record.Attempts[i].Number) == record.UsageAttempt && state == "persisted" {
				// Database acknowledgement completes a still-running attempt. Earlier
				// failure outcomes remain intact because partial usage can also be persisted.
				if record.Attempts[i].Outcome == "running" {
					record.Attempts[i].Outcome = "completed"
				}
			}
		}
	})
}

// applyProviderStatsCost attaches a provider base amount independently of user
// charges. A configured price resolver takes precedence over the separate statistics
// source, and no source leaves the existing field unchanged.
//
// The requested model substitutes for absent upstream evidence, with Qoder asking
// the resolver to prefer its requested alias. ImageCount determines request units
// when positive, otherwise one is used. The returned optional value distinguishes
// no statistics price from an explicitly zero provider expense; settlement later
// multiplies that selected base by the executing provider's rate.
func (s *Recorder) applyProviderStatsCost(ctx context.Context, row *UsageLog, providerID, groupID int64, upstream, requested, mapped string, tokens UsageTokens, at time.Time) {
	if upstream == "" {
		upstream = requested
	}
	count := 1
	if row.ImageCount > 0 {
		count = row.ImageCount
	}
	source := s.stats
	// The shared resolver combines current provider rules and catalogue pricing;
	// use the separate statistics binding only when that resolver is unavailable.
	if s.resolver != nil {
		source = s.resolver
	}
	if source == nil {
		return
	}
	row.ProviderStatsCost = source.ResolveProviderStats(ctx, billing.ProviderStatsCostInput{
		PricingAt: at, PreferRequestedModel: row.Platform == "qoder", ProviderID: providerID, GroupID: groupID, UpstreamModel: upstream, RequestedModel: requested, MappedModel: mapped, Tokens: tokens, RequestCount: count, ServiceTier: stringValueOrEmpty(row.ServiceTier), ReasoningEffort: stringValueOrEmpty(row.ReasoningEffort),
	})
}
