// 兼容入口只委托任务模块，S15/S16 清理。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/creative"
)

const CreativeOperationGenerate = native.CreativeOperationGenerate
const CreativeOperationEdit = native.CreativeOperationEdit
const CreativeOperationInpaint = native.CreativeOperationInpaint
const CreativeProvisioningPhaseCreated = native.CreativeProvisioningPhaseCreated
const CreativeProvisioningPhaseHoldReserved = native.CreativeProvisioningPhaseHoldReserved
const CreativeProvisioningPhaseTransientSaved = native.CreativeProvisioningPhaseTransientSaved
const CreativeProvisioningPhaseEnqueued = native.CreativeProvisioningPhaseEnqueued
const CreativeProvisioningPhaseFailed = native.CreativeProvisioningPhaseFailed
const CreativeProvisioningPhaseComplete = native.CreativeProvisioningPhaseComplete
const CreativeRunStatusQueued = native.CreativeRunStatusQueued
const CreativeRunStatusRunning = native.CreativeRunStatusRunning
const CreativeRunStatusProviderSucceeded = native.CreativeRunStatusProviderSucceeded
const CreativeRunStatusSettlementPending = native.CreativeRunStatusSettlementPending
const CreativeRunStatusReleasePending = native.CreativeRunStatusReleasePending
const CreativeRunStatusSucceeded = native.CreativeRunStatusSucceeded
const CreativeRunStatusFailed = native.CreativeRunStatusFailed
const CreativeRunStatusCancelled = native.CreativeRunStatusCancelled
const CreativeRunStatusResultLost = native.CreativeRunStatusResultLost
const CreativeRunOutputStatusPending = native.CreativeRunOutputStatusPending
const CreativeRunOutputStatusSucceeded = native.CreativeRunOutputStatusSucceeded
const CreativeRunOutputStatusFailed = native.CreativeRunOutputStatusFailed
const CreativeRunOutputStatusLost = native.CreativeRunOutputStatusLost
const CreativeRunOutputStatusAcked = native.CreativeRunOutputStatusAcked
const CreativeManagedBy = native.CreativeManagedBy

var ErrCreativeTransientNotFound = native.ErrCreativeTransientNotFound
var ErrCreativeTransientUnavailable = native.ErrCreativeTransientUnavailable
var ErrCreativeTransientCorrupt = native.ErrCreativeTransientCorrupt
var ErrCreativeDisabled = native.ErrCreativeDisabled
var ErrCreativeRunNotFound = native.ErrCreativeRunNotFound
var ErrCreativeRunExists = native.ErrCreativeRunExists
var ErrCreativeOutputExists = native.ErrCreativeOutputExists
var ErrCreativeWorkspaceRequired = native.ErrCreativeWorkspaceRequired
var ErrCreativeWorkspaceInvalid = native.ErrCreativeWorkspaceInvalid
var ErrCreativeInvalidTransition = native.ErrCreativeInvalidTransition
var ErrCreativeInvalidParams = native.ErrCreativeInvalidParams
var ErrCreativePromptTooLong = native.ErrCreativePromptTooLong
var ErrCreativeInvalidMime = native.ErrCreativeInvalidMime
var ErrCreativeAssetTooLarge = native.ErrCreativeAssetTooLarge
var ErrCreativeInputTooLarge = native.ErrCreativeInputTooLarge
var ErrCreativeMaskRequired = native.ErrCreativeMaskRequired
var ErrCreativeMaskSizeMismatch = native.ErrCreativeMaskSizeMismatch
var ErrCreativeOperationUnsupported = native.ErrCreativeOperationUnsupported
var ErrCreativeInvalidModel = native.ErrCreativeInvalidModel
var ErrCreativeGroupForbidden = native.ErrCreativeGroupForbidden
var ErrCreativeGroupImageDisabled = native.ErrCreativeGroupImageDisabled
var ErrCreativeRunIdempotencyConflict = native.ErrCreativeRunIdempotencyConflict
var ErrCreativeOutputNotFound = native.ErrCreativeOutputNotFound
var ErrCreativeOutputNotReady = native.ErrCreativeOutputNotReady
var ErrCreativeOutputExpired = native.ErrCreativeOutputExpired
var ErrCreativeResultLost = native.ErrCreativeResultLost
var ErrCreativeTransientFailed = native.ErrCreativeTransientFailed
var ErrCreativeBillingHoldFailed = native.ErrCreativeBillingHoldFailed
var ErrCreativeInsufficientBalance = native.ErrCreativeInsufficientBalance
var ErrCreativeSettlementBillingFail = native.ErrCreativeSettlementBillingFail

const CreativeWorkspaceHeader = native.CreativeWorkspaceHeader

type CreativeRunScope = native.CreativeRunScope

func NormalizeCreativeWorkspaceID(raw string) (string, error) {
	return native.NormalizeCreativeWorkspaceID(raw)
}
func ValidateCreativeRunScope(scope CreativeRunScope) error {
	return native.ValidateCreativeRunScope(scope)
}
func NormalizeCreativeRunScope(scope CreativeRunScope) (CreativeRunScope, error) {
	return native.NormalizeCreativeRunScope(scope)
}

type CreativeRun = native.CreativeRun
type CreativeRunOutput = native.CreativeRunOutput
type CreateCreativeRunParams = native.CreateCreativeRunParams
type CreativeRunTransitionOptions = native.CreativeRunTransitionOptions
type CreativeRunOutboxOperation = native.CreativeRunOutboxOperation

const CreativeRunOutboxProvision = native.CreativeRunOutboxProvision
const CreativeRunOutboxSettle = native.CreativeRunOutboxSettle
const CreativeRunOutboxRelease = native.CreativeRunOutboxRelease

type CreativeRunOutboxStatus = native.CreativeRunOutboxStatus

const CreativeRunOutboxPending = native.CreativeRunOutboxPending
const CreativeRunOutboxLeased = native.CreativeRunOutboxLeased
const CreativeRunOutboxDone = native.CreativeRunOutboxDone
const CreativeRunOutboxCancelled = native.CreativeRunOutboxCancelled

type CreativeRunOutbox = native.CreativeRunOutbox
type CreativeRunOutboxRepository = native.CreativeRunOutboxRepository
type CreativeRunFilter = native.CreativeRunFilter
type CreativeRunOutputBatchReader = native.CreativeRunOutputBatchReader
type CreativeRunAllowanceMarker = native.CreativeRunAllowanceMarker
type CreativeRunRepository = native.CreativeRunRepository
type CreativeRunPayload = native.CreativeRunPayload
type CreativeInputImage = native.CreativeInputImage
type CreateCreativeRunParamsPublic = native.CreateCreativeRunParamsPublic
type CreativeRunPublic = native.CreativeRunPublic
type CreativeRunOutputPublic = native.CreativeRunOutputPublic
type CreativeModelPublic = native.CreativeModelPublic
type CreativeNumericRange = native.CreativeNumericRange
type CreativeModelsResponse = native.CreativeModelsResponse
type CreativeCapabilitiesResponse = native.CreativeCapabilitiesResponse
type CreativeListRunsResponse = native.CreativeListRunsResponse

func NewCreativeRunID() (string, error)      { return native.NewCreativeRunID() }
func IsValidCreativeRunID(runID string) bool { return native.IsValidCreativeRunID(runID) }
func IsTerminalCreativeRunStatus(status string) bool {
	return native.IsTerminalCreativeRunStatus(status)
}
func IsCreativeRunSettlementPending(status string) bool {
	return native.IsCreativeRunSettlementPending(status)
}
func CanTransitionCreativeRun(from, to string) bool { return native.CanTransitionCreativeRun(from, to) }
func CreativeRunToPublic(run *CreativeRun, outputs []*CreativeRunOutput) *CreativeRunPublic {
	return native.CreativeRunToPublic(run, outputs)
}
func CreativeRunOutputToPublic(output *CreativeRunOutput) CreativeRunOutputPublic {
	return native.CreativeRunOutputToPublic(output)
}
