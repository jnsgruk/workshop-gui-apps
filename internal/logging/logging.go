// Package logging supplies command-scoped slog loggers and child output adapters.
package logging

import (
	"bytes"
	"context"
	"io"
	"log/slog"
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

const maxRecordBytes = 64 * 1024

// Writer turns child output into log records, splitting oversized lines.
// Each stream needs its own writer. Flush after the child stops writing.
type Writer struct {
	Context context.Context
	Logger  *slog.Logger
	Level   slog.Level

	line      []byte
	continued bool
}

func (w *Writer) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		room := maxRecordBytes - len(w.line)
		// Look one byte beyond the available space to accept a newline
		// immediately after a full record.
		if newline := bytes.IndexByte(p[:min(len(p), room+1)], '\n'); newline >= 0 {
			w.line = append(w.line, p[:newline]...)
			w.line = bytes.TrimSuffix(w.line, []byte("\r"))
			if len(w.line) > 0 || !w.continued {
				w.log()
			}
			w.line = w.line[:0]
			w.continued = false
			p = p[newline+1:]
			continue
		}

		take := min(room, len(p))
		w.line = append(w.line, p[:take]...)
		p = p[take:]
		if len(p) > 0 {
			w.log()
			w.line = w.line[:0]
			w.continued = true
		}
	}
	return n, nil
}

func (w *Writer) Flush() {
	if len(w.line) > 0 {
		w.log()
		w.line = w.line[:0]
	}
	w.continued = false
}

func (w *Writer) log() {
	w.Logger.Log(w.Context, w.Level, string(w.line))
}
