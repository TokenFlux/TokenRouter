package openai

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// live.go defines and validates the SDP and session-object fields required to
// create an OpenAI Live call.
// Live call creation combines an SDP description with JSON session settings.
// Validation requires nonblank SDP and a nonnull session object free of
// duplicate keys or case-variant protocol fields, including an empty object; it
// checks the existing values without trimming stored SDP or decoding the
// session's model-specific settings.

// LiveCallRequest holds client SDP and raw JSON session creation parameters.
type LiveCallRequest struct {
	// SDP and Session are required together by ValidateLiveCallRequest. SDP
	// must contain nonwhitespace text, while Session must encode a nonnull JSON
	// object; validation leaves their supplied representations unchanged.
	SDP     string          `json:"sdp"`     // client session description
	Session json.RawMessage `json:"session"` // JSON session object
}

// ValidateLiveCallRequest checks nonblank SDP and a valid, nonnull JSON session
// object without duplicate or case-ambiguous protocol fields, returning a
// field-specific error for invalid input. Nil requests fail the SDP check; an
// empty session object is accepted without modifying either field.
func ValidateLiveCallRequest(request *LiveCallRequest) error {
	if request == nil || strings.TrimSpace(request.SDP) == "" {
		return errors.New("sdp is required")
	}
	if len(request.Session) == 0 || !json.Valid(request.Session) {
		return errors.New("session must be valid JSON")
	}
	var sessionObject map[string]json.RawMessage
	if err := json.Unmarshal(request.Session, &sessionObject); err != nil {
		// Arrays and scalar JSON can be syntactically valid session values.
		// Map decoding rejects those shapes with the public session-object error
		// used by this validator.
		return errors.New("session must be a JSON object")
	}
	if sessionObject == nil {
		return errors.New("session must be a JSON object")
	}
	if err := wirejson.ValidateRequestObject(request.Session); err != nil {
		if fieldErr, ok := errors.AsType[*wirejson.RequestFieldError](err); ok {
			return fieldErr
		}
		return errors.New("session contains duplicate or ambiguous JSON fields")
	}
	return nil
}
