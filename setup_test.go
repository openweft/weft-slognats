package weftslognats

import (
	"testing"
)

// TestSetupFromEnv_NoEnv_BaseOnly : WEFT_NATS_URL unset → base-only
// handler, no panic, no error, returned closer is a no-op.
func TestSetupFromEnv_NoEnv_BaseOnly(t *testing.T) {
	t.Setenv(EnvNATSURL, "")
	log, closer := SetupFromEnv("weft.test.log")
	if log == nil {
		t.Fatal("returned logger is nil")
	}
	if closer == nil {
		t.Fatal("returned closer is nil")
	}
	if err := closer.Close(); err != nil {
		t.Errorf("noop close should not error : %v", err)
	}
	// Just verify we can call methods without panic.
	log.Info("test")
}

// TestSetupFromEnv_BadURL_Degrades : an unreachable NATS URL falls
// back to stderr-only — the operator gets a warning but the binary
// keeps running.
func TestSetupFromEnv_BadURL_Degrades(t *testing.T) {
	t.Setenv(EnvNATSURL, "nats://127.0.0.1:1")
	log, closer := SetupFromEnv("weft.test.log")
	if log == nil {
		t.Fatal("returned logger is nil")
	}
	defer closer.Close()
	log.Warn("post-degrade write should not panic")
}
