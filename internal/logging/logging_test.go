package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestWriterPreservesSplitLinesAndFlushesTrailingOutput(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil)).With("process", "child")
	ctx := WithLogger(context.Background(), logger)
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
