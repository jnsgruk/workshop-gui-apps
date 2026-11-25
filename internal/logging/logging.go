// Package logging supplies command-scoped slog loggers and child output adapters.
package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

type contextKey struct{}

type Verbosity int

const (
	Brief Verbosity = iota
	Quiet
	Verbose
)

type outputKey struct{}

type cliOutput struct {
	verbosity Verbosity
	stdout    io.Writer
	stderr    io.Writer
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

// WithCLIOutput connects user feedback and verbose diagnostics to their streams.
func WithCLIOutput(ctx context.Context, command string, verbosity Verbosity, stdout, stderr io.Writer) context.Context {
	var mu sync.Mutex
	output := cliOutput{verbosity, lockedWriter{&mu, stdout}, lockedWriter{&mu, stderr}}
	level := slog.LevelWarn
	switch verbosity {
	case Quiet:
		level = slog.LevelError
	case Verbose:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(output.stderr, &slog.HandlerOptions{Level: level})).With("command", command)
	return WithLogger(context.WithValue(ctx, outputKey{}, output), logger)
}

// Result reports a successful operation on stdout.
func Result(ctx context.Context, message string) {
	if output, ok := ctx.Value(outputKey{}).(cliOutput); ok {
		if output.verbosity != Quiet {
			_, _ = fmt.Fprintln(output.stdout, message)
		}
		return
	}
	Logger(ctx).InfoContext(ctx, message)
}

// Progress reports readiness on stderr, including diagnostic detail in verbose mode.
func Progress(ctx context.Context, message string, attrs ...any) {
	if output, ok := ctx.Value(outputKey{}).(cliOutput); ok && output.verbosity != Verbose {
		if output.verbosity == Brief {
			_, _ = fmt.Fprintln(output.stderr, message)
		}
		return
	}
	Logger(ctx).InfoContext(ctx, message, attrs...)
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

// ErrorDetail retains a bounded tail of a subprocess stream for failure messages.
type ErrorDetail struct {
	mu   sync.Mutex
	tail []byte
}

const maxErrorDetailBytes = 1024

func (d *ErrorDetail) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(p) >= maxErrorDetailBytes {
		d.tail = append(d.tail[:0], p[len(p)-maxErrorDetailBytes:]...)
		return len(p), nil
	}
	if overflow := len(d.tail) + len(p) - maxErrorDetailBytes; overflow > 0 {
		d.tail = d.tail[:copy(d.tail, d.tail[overflow:])]
	}
	d.tail = append(d.tail, p...)
	return len(p), nil
}

// Wrap adds any captured detail to err and preserves its error chain.
func (d *ErrorDetail) Wrap(err error) error {
	if err == nil {
		return nil
	}
	d.mu.Lock()
	detail := strings.TrimSpace(string(d.tail))
	d.mu.Unlock()
	if detail == "" {
		return err
	}
	return fmt.Errorf("%w: %q", err, detail)
}

// SubprocessOutput logs the two child streams and retains stderr for errors.
type SubprocessOutput struct {
	Stdout Writer
	Stderr Writer
	Detail ErrorDetail
}

func NewSubprocessOutput(ctx context.Context, logger *slog.Logger) *SubprocessOutput {
	output := &SubprocessOutput{
		Stdout: Writer{Context: ctx, Logger: logger.With("stream", "stdout"), Level: slog.LevelInfo},
		Stderr: Writer{Context: ctx, Logger: logger.With("stream", "stderr"), Level: slog.LevelInfo},
	}
	output.Stderr.Detail = &output.Detail
	return output
}

// Flush logs any trailing output after the child stops writing.
func (o *SubprocessOutput) Flush() {
	o.Stdout.Flush()
	o.Stderr.Flush()
}

const maxRecordBytes = 64 * 1024

// Writer turns child output into log records, splitting oversized lines.
// Each stream needs its own writer. Flush after the child stops writing.
type Writer struct {
	Context context.Context
	Logger  *slog.Logger
	Level   slog.Level
	Detail  *ErrorDetail

	line      []byte
	continued bool
}

func (w *Writer) Write(p []byte) (int, error) {
	n := len(p)
	if w.Detail != nil {
		_, _ = w.Detail.Write(p)
	}
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
