package sdk

import (
	"context"
	"log/slog"

	"github.com/hashicorp/go-hclog"
)

// log.go translates the author's slog records into go-plugin's structured
// stderr format. The host can therefore apply its normal log forwarding,
// rate limits, and ring buffer to SDK diagnostics without treating an entire
// JSON record as an opaque unstructured message.

// logHandler adapts immutable slog attributes to an hclog logger instance.
type logHandler struct {
	logger hclog.Logger // go-plugin structured logger sink
	attrs  []slog.Attr  // immutable inherited log attributes
	group  string       // qualified attribute group prefix
}

// Enabled reports whether the downstream logger accepts the requested level.
func (h *logHandler) Enabled(_ context.Context, level slog.Level) bool {
	return logLevel(level) >= h.logger.GetLevel()
}

// Handle forwards one record with inherited attributes and flattened groups.
// Handler copies never mutate shared attribute slices. Attribute values are
// resolved at emission time to preserve slog's deferred-value behavior.
func (h *logHandler) Handle(_ context.Context, record slog.Record) error {
	args := make([]any, 0, (len(h.attrs)+record.NumAttrs())*2)
	for _, attr := range h.attrs {
		args = appendLogAttr(args, "", attr)
	}
	record.Attrs(func(attr slog.Attr) bool { args = appendLogAttr(args, h.group, attr); return true })
	h.logger.Log(logLevel(record.Level), record.Message, args...)
	return nil
}

// WithAttrs returns a handler with an independently owned inherited attribute slice.
func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copyHandler := *h
	copyHandler.attrs = append([]slog.Attr(nil), h.attrs...)
	for _, attr := range attrs {
		if h.group != "" {
			attr.Key = h.group + attr.Key
		}
		copyHandler.attrs = append(copyHandler.attrs, attr)
	}
	return &copyHandler
}

// WithGroup qualifies subsequently added attributes without changing existing ones.
func (h *logHandler) WithGroup(name string) slog.Handler {
	copyHandler := *h
	if name != "" {
		copyHandler.group += name + "."
	}
	return &copyHandler
}

// appendLogAttr flattens nested groups into qualified hclog key-value pairs.
func appendLogAttr(args []any, prefix string, attr slog.Attr) []any {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return args
	}
	if attr.Value.Kind() == slog.KindGroup {
		if attr.Key != "" {
			prefix += attr.Key + "."
		}
		for _, nested := range attr.Value.Group() {
			args = appendLogAttr(args, prefix, nested)
		}
		return args
	}
	return append(args, prefix+attr.Key, attr.Value.Any())
}

// logLevel maps slog severity ranges to the equivalent hclog levels.
func logLevel(level slog.Level) hclog.Level {
	switch {
	case level >= slog.LevelError:
		return hclog.Error
	case level >= slog.LevelWarn:
		return hclog.Warn
	case level >= slog.LevelInfo:
		return hclog.Info
	default:
		return hclog.Debug
	}
}
