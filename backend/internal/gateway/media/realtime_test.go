package media

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

type mediaFrameStub struct {
	closed int
	order  *[]string
}

func (f *mediaFrameStub) ReadFrame(context.Context) (upstream.FrameKind, []byte, error) {
	return upstream.FrameText, nil, context.Canceled
}
func (f *mediaFrameStub) WriteFrame(context.Context, upstream.FrameKind, []byte) error { return nil }
func (f *mediaFrameStub) Close() error                                                 { f.closed++; *f.order = append(*f.order, "close"); return nil }

type realtimePortsStub struct {
	selected, released, opened, failed int
	denyWait                           bool
	credentialFailure                  bool
	firstDialFailure                   bool
	conn                               *mediaFrameStub
	order                              []string
	deadline                           time.Duration
}

func (p *realtimePortsStub) SelectRealtime(_ context.Context, _ map[int64]struct{}) (account.AccountSnapshot, bool, error) {
	p.selected++
	return account.AccountSnapshot{ID: int64(p.selected)}, true, nil
}
func (p *realtimePortsStub) AcquireRealtime(context.Context, account.AccountSnapshot) (func(), bool) {
	if p.denyWait {
		return nil, false
	}
	return func() { p.released++; p.order = append(p.order, "release") }, true
}
func (p *realtimePortsStub) RealtimeCredential(context.Context, account.AccountSnapshot) (string, error) {
	if p.credentialFailure {
		return "", errors.New("credential")
	}
	return "secret", nil
}
func (p *realtimePortsStub) OpenRealtime(ctx context.Context, _ account.AccountSnapshot, _, _ string) (upstream.FrameConn, error) {
	p.opened++
	deadline, _ := ctx.Deadline()
	p.deadline = time.Until(deadline)
	if p.firstDialFailure && p.opened == 1 {
		return nil, errors.New("dial")
	}
	p.conn = &mediaFrameStub{order: &p.order}
	return p.conn, nil
}
func (p *realtimePortsStub) RealtimeOpenFailed(context.Context, account.AccountSnapshot, error) {
	p.failed++
}

func TestRealtimeAdmissionKeepsUpstreamBeforeAcceptAndReleaseOrder(t *testing.T) {
	ports := &realtimePortsStub{firstDialFailure: true}
	result := OpenRealtime(context.Background(), "voice", 12*time.Second, ports)
	require.NotNil(t, result.Lease)
	require.Equal(t, 2, ports.selected)
	require.Equal(t, 1, ports.released)
	require.Equal(t, 1, ports.failed)
	require.Greater(t, ports.deadline, 11*time.Second)
	require.LessOrEqual(t, ports.deadline, 12*time.Second)
	require.NoError(t, result.Lease.Close())
	require.NoError(t, result.Lease.Close())
	require.Equal(t, 1, ports.conn.closed)
	require.Equal(t, 2, ports.released)
	require.Equal(t, []string{"release", "close", "release"}, ports.order)
}
func TestRealtimeAdmissionCredentialFailureAndWaiting(t *testing.T) {
	ports := &realtimePortsStub{credentialFailure: true}
	result := OpenRealtime(context.Background(), "voice", time.Second, ports)
	require.Nil(t, result.Lease)
	require.True(t, result.CandidateSeen)
	require.Equal(t, 4, ports.selected)
	require.Equal(t, 4, ports.released)
	require.Zero(t, ports.opened)
	waiting := &realtimePortsStub{denyWait: true}
	result = OpenRealtime(context.Background(), "voice", time.Second, waiting)
	require.True(t, result.WaitRejected)
	require.Zero(t, waiting.opened)
	require.Zero(t, waiting.released)
}

type voicePortsStub struct {
	selected, released, completed int
	outcomes                      []VoiceOutcome
}

func (p *voicePortsStub) SelectVoice(context.Context, map[int64]struct{}) (account.AccountSnapshot, bool, error) {
	p.selected++
	return account.AccountSnapshot{ID: int64(p.selected)}, true, nil
}
func (p *voicePortsStub) AcquireVoice(context.Context, account.AccountSnapshot) (func(), bool) {
	return func() { p.released++ }, true
}
func (p *voicePortsStub) ForwardVoice(context.Context, account.AccountSnapshot, VoiceRequest) VoiceOutcome {
	return p.outcomes[p.selected-1]
}
func (p *voicePortsStub) CompleteVoice(context.Context, account.AccountSnapshot, VoiceRequest, *VoiceResult) {
	p.completed++
}
func TestVoiceRetryUsesOriginalFourAttemptBudget(t *testing.T) {
	failure := VoiceOutcome{Err: errors.New("upstream"), RetryNext: true}
	ports := &voicePortsStub{outcomes: []VoiceOutcome{failure, failure, failure, failure}}
	result := RunVoice(context.Background(), VoiceRequest{Endpoint: "tts"}, ports)
	require.NotNil(t, result)
	require.ErrorIs(t, result.Last, failure.Err)
	require.Equal(t, 4, ports.selected)
	require.Equal(t, 4, ports.released)
	require.Zero(t, ports.completed)
	success := &voicePortsStub{outcomes: []VoiceOutcome{failure, {Result: &VoiceResult{RequestID: "audio"}}}}
	require.Nil(t, RunVoice(context.Background(), VoiceRequest{Endpoint: "tts"}, success))
	require.Equal(t, 2, success.released)
	require.Equal(t, 1, success.completed)
}
