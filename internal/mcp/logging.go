package mcp

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
)

type loggerKey struct{}

var requestCounter uint64

// InitLogging configures a global JSON logger writing strictly to the provided writer (typically os.Stderr).
func InitLogging(w io.Writer) {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Key = "ts"
			}
			return a
		},
	})
	slog.SetDefault(slog.New(handler))
}

// generateRequestID returns a monotonically increasing request ID.
func generateRequestID() uint64 {
	return atomic.AddUint64(&requestCounter, 1)
}

// WithLogger attaches a request-scoped logger to the context.
func WithLogger(ctx context.Context, reqID uint64, toolName string) context.Context {
	logger := slog.Default().With("request_id", reqID, "tool", toolName)
	return context.WithValue(ctx, loggerKey{}, logger)
}

// LoggerFrom retrieves the request-scoped logger from the context.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// LogToolStart emits a structured log when a tool begins execution.
func LogToolStart(ctx context.Context) {
	logger := LoggerFrom(ctx)
	logger.Info("tool.start")
}

// LogToolEnd emits a structured log when a tool finishes execution.
func LogToolEnd(ctx context.Context, durationMs int64, err error) {
	logger := LoggerFrom(ctx)
	if err != nil {
		logger.Error("tool.end", "duration_ms", durationMs, "err", err.Error())
	} else {
		logger.Info("tool.end", "duration_ms", durationMs)
	}
}

// LogStage emits a structured log tracking the duration of internal pipeline stages.
func LogStage(ctx context.Context, stage string, durationMs int64) {
	logger := LoggerFrom(ctx)
	logger.Info("pipeline.stage", "stage", stage, "duration_ms", durationMs)
}
