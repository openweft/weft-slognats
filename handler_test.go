package weftslognats

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// fakePublisher captures Publish calls for assertion.
type fakePublisher struct {
	mu      sync.Mutex
	calls   []publish
	failErr error
}

type publish struct {
	subject string
	data    string
}

func (p *fakePublisher) Publish(subject string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, publish{subject: subject, data: string(data)})
	return p.failErr
}

func (p *fakePublisher) snapshot() []publish {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]publish, len(p.calls))
	copy(out, p.calls)
	return out
}

// TestHandler_INFOGoesToBaseOnly : INFO is below the WARN floor, so
// it lands on stderr but NOT on NATS. The classic noise filter.
func TestHandler_INFOGoesToBaseOnly(t *testing.T) {
	var stderr bytes.Buffer
	pub := &fakePublisher{}
	h := NewHandler(Options{
		Base:    slog.NewJSONHandler(&stderr, nil),
		Conn:    pub,
		Subject: "weft.test.log",
	})
	log := slog.New(h)
	log.Info("just booted", "phase", "init")

	if !strings.Contains(stderr.String(), "just booted") {
		t.Errorf("base did not receive INFO record: %q", stderr.String())
	}
	if calls := pub.snapshot(); len(calls) != 0 {
		t.Errorf("NATS got %d publishes for INFO ; want 0", len(calls))
	}
}

// TestHandler_WARNFansOut : WARN level matches the floor, so it
// goes to BOTH stderr and NATS. The payload on NATS is identical
// to the JSONHandler stderr line (minus trailing newline).
func TestHandler_WARNFansOut(t *testing.T) {
	var stderr bytes.Buffer
	pub := &fakePublisher{}
	h := NewHandler(Options{
		Base:    slog.NewJSONHandler(&stderr, nil),
		Conn:    pub,
		Subject: "weft.test.log",
	})
	log := slog.New(h)
	log.Warn("VM start retrying", "vm_uuid", "abc", "attempt", 3)

	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "VM start retrying") {
		t.Errorf("base missing WARN record: %q", stderrStr)
	}
	calls := pub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("NATS got %d publishes ; want 1", len(calls))
	}
	if calls[0].subject != "weft.test.log" {
		t.Errorf("subject = %q", calls[0].subject)
	}
	// Payload should parse as JSON with the same fields.
	var got map[string]any
	if err := json.Unmarshal([]byte(calls[0].data), &got); err != nil {
		t.Fatalf("payload not JSON: %v / %q", err, calls[0].data)
	}
	if got["level"] != "WARN" {
		t.Errorf("payload.level = %v", got["level"])
	}
	if got["msg"] != "VM start retrying" {
		t.Errorf("payload.msg = %v", got["msg"])
	}
	if got["vm_uuid"] != "abc" {
		t.Errorf("payload.vm_uuid = %v", got["vm_uuid"])
	}
}

// TestHandler_NilConn_BaseOnly : conn=nil is the supported degrade
// mode. WARN still goes to stderr ; no panic, no error.
func TestHandler_NilConn_BaseOnly(t *testing.T) {
	var stderr bytes.Buffer
	h := NewHandler(Options{
		Base: slog.NewJSONHandler(&stderr, nil),
		// Conn nil, Subject nil — explicitly degraded.
	})
	log := slog.New(h)
	log.Error("disk full")
	if !strings.Contains(stderr.String(), "disk full") {
		t.Errorf("base did not receive ERROR record: %q", stderr.String())
	}
}

// TestHandler_PublishErrorDoesNotBubble : a NATS Publish failure
// invokes OnPublishError but the Logger.<level>() call still returns
// normally — we never want logging to fail the caller.
func TestHandler_PublishErrorDoesNotBubble(t *testing.T) {
	pub := &fakePublisher{failErr: errors.New("nats unreachable")}
	var capturedErr error
	var mu sync.Mutex
	h := NewHandler(Options{
		Base:    slog.NewJSONHandler(io.Discard, nil),
		Conn:    pub,
		Subject: "weft.test.log",
		OnPublishError: func(err error) {
			mu.Lock()
			capturedErr = err
			mu.Unlock()
		},
	})
	log := slog.New(h)
	log.Error("something broke")
	mu.Lock()
	defer mu.Unlock()
	if capturedErr == nil {
		t.Error("OnPublishError was not invoked on Publish failure")
	}
}

// TestHandler_WithAttrsPropagates : attrs added via Logger.With()
// must land in BOTH stderr and NATS payloads. Otherwise the
// per-request enrichment that operators rely on disappears in
// the diagnosis pipeline.
func TestHandler_WithAttrsPropagates(t *testing.T) {
	var stderr bytes.Buffer
	pub := &fakePublisher{}
	h := NewHandler(Options{
		Base:    slog.NewJSONHandler(&stderr, nil),
		Conn:    pub,
		Subject: "weft.test.log",
	})
	log := slog.New(h).With("host_uuid", "host-001", "dc", "dc1")
	log.Warn("driver crashed")

	if !strings.Contains(stderr.String(), `"host_uuid":"host-001"`) {
		t.Errorf("base missing host_uuid attr: %q", stderr.String())
	}
	calls := pub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("NATS got %d publishes ; want 1", len(calls))
	}
	if !strings.Contains(calls[0].data, `"host_uuid":"host-001"`) {
		t.Errorf("NATS payload missing host_uuid attr: %q", calls[0].data)
	}
	if !strings.Contains(calls[0].data, `"dc":"dc1"`) {
		t.Errorf("NATS payload missing dc attr: %q", calls[0].data)
	}
}

// TestHandler_NilBase_Panics : Options.Base is required. A silent
// drop on nil would mask a config bug ; better to fail loudly at
// startup.
func TestHandler_NilBase_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewHandler with nil Base did not panic")
		}
	}()
	_ = NewHandler(Options{})
}

// TestHandler_ConnWithoutSubject_Panics : same rationale — a
// half-config is a config bug, fail at startup.
func TestHandler_ConnWithoutSubject_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewHandler with Conn but no Subject did not panic")
		}
	}()
	_ = NewHandler(Options{
		Base: slog.NewJSONHandler(io.Discard, nil),
		Conn: &fakePublisher{},
	})
}

// TestHandler_PayloadHasNoTrailingNewline : JSONHandler appends \n
// after each record ; we strip it before publishing because NATS
// is record-oriented and the receiver (weft-doctor) treats one
// message as one record.
func TestHandler_PayloadHasNoTrailingNewline(t *testing.T) {
	pub := &fakePublisher{}
	h := NewHandler(Options{
		Base:    slog.NewJSONHandler(io.Discard, nil),
		Conn:    pub,
		Subject: "weft.test.log",
	})
	log := slog.New(h)
	log.Error("test")

	calls := pub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("want 1 publish")
	}
	if strings.HasSuffix(calls[0].data, "\n") {
		t.Errorf("payload should not end with newline: %q", calls[0].data)
	}
}

// TestHandler_PayloadIsCompatibleWithSlogJSONHandler : the receiver
// (weft-doctor's ingest.parseSlog) parses the standard JSONHandler
// format. The contract test : our NATS payload is byte-identical
// to a same-event JSONHandler stderr line (minus newline).
func TestHandler_PayloadIsCompatibleWithSlogJSONHandler(t *testing.T) {
	// Baseline : plain JSONHandler to a buffer.
	var baseline bytes.Buffer
	plain := slog.New(slog.NewJSONHandler(&baseline, nil))

	// Under-test : NATS handler with a base that also writes to a
	// buffer + a publisher that captures the payload.
	var stderr bytes.Buffer
	pub := &fakePublisher{}
	h := NewHandler(Options{
		Base:    slog.NewJSONHandler(&stderr, nil),
		Conn:    pub,
		Subject: "weft.test.log",
	})
	wrapped := slog.New(h)

	// Same message + attrs.
	plain.Error("disk full", "device", "sda")
	wrapped.Error("disk full", "device", "sda")

	// The NATS payload should equal the baseline JSON line (sans \n)
	// modulo the time field (different timestamp). Strip time before
	// comparing.
	baselineLine := strings.TrimRight(baseline.String(), "\n")
	natsPayload := pub.snapshot()[0].data

	baselineNoTime := stripTime(t, baselineLine)
	natsNoTime := stripTime(t, natsPayload)
	if baselineNoTime != natsNoTime {
		t.Errorf("payload diverges from baseline JSONHandler :\n  base: %s\n  nats: %s", baselineNoTime, natsNoTime)
	}
}

func stripTime(t *testing.T, line string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("not JSON: %v / %q", err, line)
	}
	delete(m, "time")
	b, _ := json.Marshal(m)
	return string(b)
}
