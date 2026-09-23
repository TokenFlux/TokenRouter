package usage_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestParseUsageRequestType(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		input   string
		want    usage.RequestType
		wantErr bool
	}

	cases := []testCase{
		{name: "unknown", input: "unknown", want: usage.RequestTypeUnknown},
		{name: "sync", input: "sync", want: usage.RequestTypeSync},
		{name: "stream", input: "stream", want: usage.RequestTypeStream},
		{name: "ws_v2", input: "ws_v2", want: usage.RequestTypeWSV2},
		{name: "cyber", input: "cyber", want: usage.RequestTypeCyberBlocked},
		{name: "case_insensitive", input: "WS_V2", want: usage.RequestTypeWSV2},
		{name: "trim_spaces", input: "  stream  ", want: usage.RequestTypeStream},
		{name: "invalid", input: "xxx", wantErr: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := usage.ParseUsageRequestType(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestRequestTypeNormalizeAndString(t *testing.T) {
	t.Parallel()

	require.Equal(t, usage.RequestTypeUnknown, usage.RequestType(99).Normalize())
	require.Equal(t, "unknown", usage.RequestType(99).String())
	require.Equal(t, "sync", usage.RequestTypeSync.String())
	require.Equal(t, "stream", usage.RequestTypeStream.String())
	require.Equal(t, "ws_v2", usage.RequestTypeWSV2.String())
	require.Equal(t, "cyber", usage.RequestTypeCyberBlocked.String())
}

func TestRequestTypeFromLegacy(t *testing.T) {
	t.Parallel()

	require.Equal(t, usage.RequestTypeWSV2, usage.RequestTypeFromLegacy(false, true))
	require.Equal(t, usage.RequestTypeStream, usage.RequestTypeFromLegacy(true, false))
	require.Equal(t, usage.RequestTypeSync, usage.RequestTypeFromLegacy(false, false))
}

func TestApplyLegacyRequestFields(t *testing.T) {
	t.Parallel()

	stream, ws := usage.ApplyLegacyRequestFields(usage.RequestTypeSync, true, true)
	require.False(t, stream)
	require.False(t, ws)

	stream, ws = usage.ApplyLegacyRequestFields(usage.RequestTypeStream, false, true)
	require.True(t, stream)
	require.False(t, ws)

	stream, ws = usage.ApplyLegacyRequestFields(usage.RequestTypeWSV2, false, false)
	require.True(t, stream)
	require.True(t, ws)

	stream, ws = usage.ApplyLegacyRequestFields(usage.RequestTypeCyberBlocked, true, true)
	require.True(t, stream)
	require.True(t, ws)

	stream, ws = usage.ApplyLegacyRequestFields(usage.RequestTypeUnknown, true, false)
	require.True(t, stream)
	require.False(t, ws)
}

func TestUsageLogSyncRequestTypeAndLegacyFields(t *testing.T) {
	t.Parallel()

	log := &usage.UsageLog{RequestType: usage.RequestTypeWSV2, Stream: false, OpenAIWSMode: false}
	log.SyncRequestTypeAndLegacyFields()

	require.Equal(t, usage.RequestTypeWSV2, log.RequestType)
	require.True(t, log.Stream)
	require.True(t, log.OpenAIWSMode)
}

func TestUsageLogEffectiveRequestTypeFallback(t *testing.T) {
	t.Parallel()

	log := &usage.UsageLog{RequestType: usage.RequestTypeUnknown, Stream: true, OpenAIWSMode: true}
	require.Equal(t, usage.RequestTypeWSV2, log.EffectiveRequestType())
}

func TestUsageLogEffectiveRequestTypeNilReceiver(t *testing.T) {
	t.Parallel()

	var log *usage.UsageLog
	require.Equal(t, usage.RequestTypeUnknown, log.EffectiveRequestType())
}

func TestUsageLogSyncRequestTypeAndLegacyFieldsNilReceiver(t *testing.T) {
	t.Parallel()

	var log *usage.UsageLog
	log.SyncRequestTypeAndLegacyFields()
}
