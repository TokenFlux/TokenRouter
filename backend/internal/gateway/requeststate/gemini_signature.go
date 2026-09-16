package requeststate

import "bytes"

// GeminiSignatureState 只属于一个请求，保留账号切换与未知绑定的一次清理语义。
type GeminiSignatureState struct {
	BoundAccountID int64
	cleanedUnknown bool
}
type SignatureChange struct {
	Clean             bool
	Missing           bool
	PreviousAccountID int64
}

func (s *GeminiSignatureState) Select(accountID int64, hasSessionKey bool, body []byte) SignatureChange {
	change := SignatureChange{PreviousAccountID: s.BoundAccountID}
	if s.BoundAccountID > 0 && s.BoundAccountID != accountID {
		change.Clean = true
		s.BoundAccountID = accountID
	} else if hasSessionKey && s.BoundAccountID == 0 && !s.cleanedUnknown && bytes.Contains(body, []byte(`"thoughtSignature"`)) {
		change.Clean = true
		change.Missing = true
		s.cleanedUnknown = true
		s.BoundAccountID = accountID
	} else if s.BoundAccountID == 0 {
		s.BoundAccountID = accountID
	}
	return change
}
