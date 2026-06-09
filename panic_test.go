package weftslognats

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestPanicReporter_LogsAndRepanics verifies that PanicReporter
// captures the panic value via slog.Error and re-panics so the
// process still exits abnormally. We swap slog.Default() for a
// buffer-backed handler so the test can inspect the emitted JSON.
func TestPanicReporter_LogsAndRepanics(t *testing.T) {
	var buf bytes.Buffer
	original := slog.Default()
	defer slog.SetDefault(original)
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("PanicReporter must re-panic so systemd sees the crash")
		}
		if r != "boom" {
			t.Errorf("re-panic value: got %v, want \"boom\"", r)
		}
		out := buf.String()
		if !strings.Contains(out, "panic in main") {
			t.Errorf("missing panic log line: %s", out)
		}
		if !strings.Contains(out, "boom") {
			t.Errorf("missing panic value in log: %s", out)
		}
	}()
	func() {
		defer PanicReporter("weft-test")
		panic("boom")
	}()
}

// TestPanicReporter_DefaultName falls back to "weft" when no name is given.
func TestPanicReporter_DefaultName(t *testing.T) {
	var buf bytes.Buffer
	original := slog.Default()
	defer slog.SetDefault(original)
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	defer func() {
		_ = recover()
		out := buf.String()
		if !strings.Contains(out, "weft : panic in main") {
			t.Errorf("expected default name prefix \"weft\" : %s", out)
		}
	}()
	func() {
		defer PanicReporter()
		panic("x")
	}()
}

// TestPanicReporter_NoPanicNoLog verifies the no-op path.
func TestPanicReporter_NoPanicNoLog(t *testing.T) {
	var buf bytes.Buffer
	original := slog.Default()
	defer slog.SetDefault(original)
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	func() {
		defer PanicReporter()
	}()
	if buf.Len() != 0 {
		t.Errorf("expected no log without panic, got: %s", buf.String())
	}
}
