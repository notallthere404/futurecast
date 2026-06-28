package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// Tx is the process-wide log broadcaster. The server constructs it
// once at boot and shares it with the SSE log endpoint so dashboard
// clients see live logs as the server emits them.
var Tx = NewBroadcaster()

// BroadcastHandler wraps a slog.Handler so every record is mirrored
// onto the Broadcaster in addition to being written by the underlying
// handler (stdout text in production).
type BroadcastHandler struct {
	slog.Handler
	tx *Broadcaster
}

func (h *BroadcastHandler) Handle(ctx context.Context, r slog.Record) error {
	msg := fmt.Sprintf("[%s] %s %s", r.Time.Format("15:04:05"), r.Level, r.Message)
	h.tx.Send(msg)
	return h.Handler.Handle(ctx, r)
}

// New builds the application logger. `level` is one of debug | info |
// warn | error; anything else falls back to info. Caller reads the
// level from the config snapshot before constructing the logger.
func New(service, level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	std := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(&BroadcastHandler{Handler: std, tx: Tx})
}
