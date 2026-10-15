// Package logging supplies command-scoped slog loggers and child output adapters.
package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

type contextKey struct{}

func New(output io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(output, nil))
}

func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, logger)
}

func Logger(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(contextKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// Writer turns child output into log records. Each stream needs its own writer;
// Flush must be called after the child has finished writing.
type Writer struct {
	Logger *slog.Logger
	Level  slog.Level
	line   string
}

func (w *Writer) Write(p []byte) (int, error) {
	w.line += string(p)
	for {
		i := strings.IndexByte(w.line, '\n')
		if i < 0 {
			// Bound buffering for children that never emit a newline.
			if len(w.line) >= 64*1024 {
				w.Flush()
			}
			return len(p), nil
		}
		w.Logger.Log(context.Background(), w.Level, strings.TrimSuffix(w.line[:i], "\r"))
		w.line = w.line[i+1:]
	}
}

func (w *Writer) Flush() {
	if w.line != "" {
		w.Logger.Log(context.Background(), w.Level, w.line)
		w.line = ""
	}
}
