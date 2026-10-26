package logger

import (
	"log/slog"
	"os"
)

// New creates a new structured logger.
func New() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, nil))
}

// RequestID returns a slog.Attr for the request ID.
func RequestID(id string) slog.Attr {
	return slog.String("request_id", id)
}
