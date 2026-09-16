package service

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

const LiveControllerPending = session.LiveControllerPending
const LiveControllerObserver = session.LiveControllerObserver
const LiveControllerProxy = session.LiveControllerProxy
const LiveControllerClosed = session.LiveControllerClosed

var ErrLiveUnavailable = session.ErrLiveUnavailable
var ErrLiveConcurrencyFull = session.ErrLiveConcurrencyFull
var ErrLiveCallNotFound = session.ErrLiveCallNotFound
var ErrLiveIdentityMismatch = session.ErrLiveIdentityMismatch
var ErrLiveControllerChanged = session.ErrLiveControllerChanged
var ErrLiveClientPolicyDenied = session.ErrLiveClientPolicyDenied

type LiveAttestationUnavailableError = session.LiveAttestationUnavailableError

type LiveCallRequest = session.LiveCallRequest

type LiveCallIdentity = session.LiveCallIdentity

type LiveCallRecord = session.LiveCallRecord

// LiveCallCreated 表示上游成功创建的 Live 会话。
type LiveCallCreated struct {
	SDP      []byte
	CallID   string
	Location string
	Account  *Account
}

type LiveCallStore = session.LiveCallStore

// Live 并发租约契约由 scheduler 拥有；远端会话记录仍留执行层。
type LiveConcurrencyCache = scheduler.LiveConcurrencyCache
