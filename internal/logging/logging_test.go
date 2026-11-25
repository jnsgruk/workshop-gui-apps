package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestWriterPreservesSplitLinesAndFlushesTrailingOutput(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil)).With("process", "child")
	ctx := WithLogger(t.Context(), logger)
	w := &Writer{Context: ctx, Logger: Logger(ctx), Level: slog.LevelWarn}
	for _, part := range []string{"fir", "st\r\nsecond\ntrai", "ling"} {
		if n, err := w.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatalf("write: %d, %v", n, err)
		}
	}
	w.Flush()
	w.Flush()
	decoder := json.NewDecoder(&output)
	for _, message := range []string{"first", "second", "trailing"} {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		if record["msg"] != message || record["level"] != "WARN" || record["process"] != "child" {
			t.Fatalf("unexpected record: %v", record)
		}
	}
	if decoder.More() {
		t.Fatal("unexpected extra log records")
	}
}

func TestWriterBoundsOversizedRecords(t *testing.T) {
	var output bytes.Buffer
	ctx := t.Context()
	w := &Writer{Context: ctx, Logger: slog.New(slog.NewJSONHandler(&output, nil)), Level: slog.LevelInfo}
	full := strings.Repeat("x", maxRecordBytes)
	for _, part := range []string{full + full + "tail\n", full + "\r", "\n\n"} {
		if n, err := w.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatalf("write: %d, %v", n, err)
		}
	}
	w.Flush()

	decoder := json.NewDecoder(&output)
	for i, want := range []string{full, full, "tail", full, ""} {
		var record struct {
			Message string `json:"msg"`
		}
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		if record.Message != want {
			t.Fatalf("record %d: got %d bytes, want %d", i, len(record.Message), len(want))
		}
	}
	if decoder.More() {
		t.Fatal("unexpected extra log records")
	}
}

func TestCLIOutputVerbosity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		verbosity Verbosity
		stdout    string
	}{
		{"brief", Brief, "Connected.\n"},
		{"quiet", Quiet, ""},
		{"verbose", Verbose, "Connected.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			ctx := WithCLIOutput(t.Context(), "host connect", tc.verbosity, &stdout, &stderr)
			Result(ctx, "Connected.")
			Progress(ctx, "Ready.", "socket", "/test/socket")
			Logger(ctx).InfoContext(ctx, "Child diagnostic.", "stream", "stderr")
			Logger(ctx).WarnContext(ctx, "Cannot read helper diagnostics.")
			if stdout.String() != tc.stdout {
				t.Fatalf("stdout = %q, want %q", stdout.String(), tc.stdout)
			}

			switch tc.verbosity {
			case Quiet:
				if stderr.Len() != 0 {
					t.Fatalf("quiet stderr = %q, want empty", stderr.String())
				}
			case Brief:
				if !strings.HasPrefix(stderr.String(), "Ready.\n") ||
					!strings.Contains(stderr.String(), "Cannot read helper diagnostics.") ||
					strings.Contains(stderr.String(), "Child diagnostic.") {
					t.Fatalf("brief stderr = %q, want readiness and warning", stderr.String())
				}
			case Verbose:
				lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
				if len(lines) != 3 || !strings.Contains(lines[0], "socket=/test/socket") ||
					!strings.Contains(lines[1], "stream=stderr") || !strings.Contains(lines[2], "level=WARN") {
					t.Fatalf("verbose stderr = %q, want structured readiness and diagnostic", stderr.String())
				}
				for _, line := range lines {
					timestamp, _, _ := strings.Cut(strings.TrimPrefix(line, "time="), " ")
					if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
						t.Fatalf("invalid log timestamp in %q: %v", line, err)
					}
				}
			}
		})
	}
}

func TestErrorDetailRetainsBoundedTail(t *testing.T) {
	var detail ErrorDetail
	for _, part := range []string{strings.Repeat("x", 2*maxErrorDetailBytes), "first\n", "second\r\n"} {
		if n, err := detail.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatalf("write error detail: %d, %v", n, err)
		}
	}
	wantErr := errors.New("exit status 4")
	err := detail.Wrap(wantErr)
	if !errors.Is(err, wantErr) || !strings.HasSuffix(err.Error(), `first\nsecond"`) {
		t.Fatalf("unexpected wrapped error %q", err)
	}
	if len(detail.tail) != maxErrorDetailBytes || strings.ContainsAny(err.Error(), "\r\n") {
		t.Fatalf("invalid error detail length or line breaks: %q", err)
	}
}
