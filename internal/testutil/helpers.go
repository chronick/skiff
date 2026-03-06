package testutil

import (
	"io"
	"log/slog"

	"github.com/chronick/plane/internal/logbuf"
	"github.com/chronick/plane/internal/status"
)

// NewTestLogger returns a silent logger for tests.
func NewTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// NewTestState returns a fresh SharedState.
func NewTestState() *status.SharedState {
	return status.NewSharedState()
}

// NewTestLogBuffer returns a LogBuffer with a small capacity.
func NewTestLogBuffer() *logbuf.LogBuffer {
	return logbuf.New(100)
}
