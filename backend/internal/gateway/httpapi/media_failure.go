package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// MediaNoAccount 是共同选路分类的只读结果，不把路由查询实现带入 HTTP 适配。
type MediaNoAccount struct {
	ModelNotFound bool
	Status        int
	Type, Message string
}
type MediaFailureContext struct {
	Grok, Generation, StreamStarted bool
	BoundAccountID, AccountID       int64
	WriterBefore                    int
	Context                         *gin.Context
	Log                             *zap.Logger
}
type MediaFailurePorts interface {
	MediaClassify() MediaNoAccount
	MediaNoAvailable(error) bool
	MediaCapacity(error, bool)
	MediaError(int, string, string, bool)
	MediaFailover(error, bool)
	MediaSimpleExhausted()
	MediaForwardCyber(error) bool
	MediaCyber(error)
	MediaReportUnexpected(*media.GenerationResult, error)
	MediaCommunicated(error) bool
	MediaEnsureFallback(error) bool
	MediaWarnFailure(bool) bool
}

// WriteGenerationFailure 保留流输出、上游分类与错误展示边界；不参与选号或资金操作。
func WriteGenerationFailure(f media.GenerationFailure, state MediaFailureContext, p MediaFailurePorts) {
	if state.Grok {
		writeGrokGenerationFailure(f, state, p)
		return
	}
	switch f.Stage {
	case "selection", "empty_selection":
		if f.Stage == "empty_selection" || f.Excluded == 0 {
			classification := p.MediaClassify()
			if !classification.ModelNotFound {
				p.MediaCapacity(f.Err, f.Stage != "empty_selection")
			}
			message := classification.Message
			if !classification.ModelNotFound {
				message = "No available compatible accounts"
			}
			p.MediaError(classification.Status, classification.Type, message, true)
			return
		}
		if f.Outcome.Failure != nil {
			p.MediaFailover(f.Outcome.Err, state.StreamStarted)
		} else {
			p.MediaSimpleExhausted()
		}
	case "exhausted":
		p.MediaFailover(f.Outcome.Err, f.Outcome.OutputChanged || state.StreamStarted)
	case "unexpected":
		recorded := p.MediaForwardCyber(f.Err)
		if !recorded {
			p.MediaCyber(f.Err)
		}
		p.MediaReportUnexpected(f.Outcome.Result, f.Err)
		communicated := p.MediaCommunicated(f.Err)
		fallback := false
		if !communicated && (!recorded || state.Context.Writer.Size() == state.WriterBefore) {
			fallback = p.MediaEnsureFallback(f.Err)
		}
		fields := []zap.Field{zap.Int64("account_id", state.AccountID), zap.Bool("fallback_error_response_written", fallback), zap.Bool("upstream_error_response_already_written", communicated), zap.Error(f.Err)}
		if p.MediaWarnFailure(fallback) {
			state.Log.Warn("openai.images.forward_failed", fields...)
		} else {
			state.Log.Error("openai.images.forward_failed", fields...)
		}
	}
}
func writeGrokGenerationFailure(f media.GenerationFailure, state MediaFailureContext, p MediaFailurePorts) {
	switch f.Stage {
	case "ineligible":
		p.MediaCapacity(nil, false)
		p.MediaError(503, "grok_media_no_eligible_account", "No eligible Grok media accounts", false)
	case "bound_unavailable":
		state.Log.Warn("grok_media.video_lookup_bound_account_unavailable", zap.Int64("bound_account_id", state.BoundAccountID), zap.Int64("selected_account_id", f.SelectedID))
		p.MediaError(404, "not_found_error", "Video request not found", false)
	case "selection", "empty_selection":
		if state.Generation && (f.Stage == "empty_selection" || (p.MediaNoAvailable(f.Err) && (f.Excluded == 0 || (f.EligibilityRejected && f.Outcome.Err == nil)))) {
			p.MediaCapacity(f.Err, f.Stage != "empty_selection")
			p.MediaError(503, "grok_media_no_eligible_account", "No eligible Grok media accounts", false)
			return
		}
		if f.Stage == "empty_selection" || f.Excluded == 0 {
			classification := p.MediaClassify()
			if !classification.ModelNotFound {
				p.MediaCapacity(f.Err, f.Stage != "empty_selection")
			}
			p.MediaError(classification.Status, classification.Type, classification.Message, false)
			return
		}
		if f.Outcome.Failure != nil {
			p.MediaFailover(f.Outcome.Err, false)
		} else {
			p.MediaError(502, "api_error", "Upstream request failed", false)
		}
	case "exhausted":
		p.MediaFailover(f.Outcome.Err, f.Outcome.OutputChanged)
	case "unexpected":
		if !p.MediaCommunicated(f.Err) && !f.Outcome.OutputChanged {
			p.MediaError(502, "upstream_error", "Upstream request failed", false)
		}
		state.Log.Warn("grok_media.forward_failed", zap.Int64("account_id", state.AccountID), zap.Error(f.Err))
	}
}
