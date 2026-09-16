package ws

import "sync"

// TurnLifecycle 串行化终态写出和下一轮接受；上行与下行 relay 仍并行。
type TurnLifecycle struct {
	mu       sync.Mutex
	inFlight bool
}

func NewTurnLifecycle(inFlight bool) *TurnLifecycle {
	return &TurnLifecycle{inFlight: inFlight}
}

func (l *TurnLifecycle) BeginResponseCreate(onAccepted func()) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight {
		return false
	}
	l.inFlight = true
	if onAccepted != nil {
		onAccepted()
	}
	return true
}

func (l *TurnLifecycle) CancelResponseCreate() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.inFlight = false
	l.mu.Unlock()
}

func (l *TurnLifecycle) BeginTerminalWrite() {
	if l != nil {
		l.mu.Lock()
	}
}

func (l *TurnLifecycle) FinishTerminalWrite(succeeded bool, onSucceeded func()) {
	if l == nil {
		return
	}
	if succeeded {
		if onSucceeded != nil {
			onSucceeded()
		}
		l.inFlight = false
	}
	l.mu.Unlock()
}
