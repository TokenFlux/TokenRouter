package httpapi

import (
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mediaFailureProbe struct {
	MediaFailurePorts
	result               MediaNoAccount
	status               int
	typ, message         string
	stream, conditional  bool
	classified, capacity int
}

func (p *mediaFailureProbe) MediaClassify() MediaNoAccount { p.classified++; return p.result }
func (p *mediaFailureProbe) MediaCapacity(_ error, conditional bool) {
	p.capacity++
	p.conditional = conditional
}
func (p *mediaFailureProbe) MediaError(status int, typ, message string, stream bool) {
	p.status = status
	p.typ = typ
	p.message = message
	p.stream = stream
}
func (p *mediaFailureProbe) MediaNoAvailable(error) bool { return true }
func TestMediaFailureHTTPPreservesImageCapacityAndGrokEligibility(t *testing.T) {
	p := &mediaFailureProbe{result: MediaNoAccount{Status: 503, Type: "api_error", Message: "original"}}
	WriteGenerationFailure(media.GenerationFailure{Stage: "empty_selection"}, MediaFailureContext{Log: zap.NewNop()}, p)
	require.Equal(t, 503, p.status)
	require.Equal(t, "No available compatible accounts", p.message)
	require.True(t, p.stream)
	require.False(t, p.conditional)
	p = &mediaFailureProbe{}
	WriteGenerationFailure(media.GenerationFailure{Stage: "selection", Err: errors.New("no account"), EligibilityRejected: true, Excluded: 2}, MediaFailureContext{Grok: true, Generation: true, Log: zap.NewNop()}, p)
	require.Equal(t, 503, p.status)
	require.Equal(t, "grok_media_no_eligible_account", p.typ)
	require.Zero(t, p.classified)
	require.True(t, p.conditional)
}
