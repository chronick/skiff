package logbuf

import (
	"testing"
)

func TestAppendAndLines(t *testing.T) {
	buf := New(10)

	buf.Append("svc1", "INFO starting up")
	buf.Append("svc1", "ERROR something broke")
	buf.Append("svc2", "WARN disk space low")

	lines := buf.Lines("", 0, "")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
}

func TestFilterBySource(t *testing.T) {
	buf := New(10)

	buf.Append("svc1", "hello")
	buf.Append("svc2", "world")
	buf.Append("svc1", "again")

	lines := buf.Lines("svc1", 0, "")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines for svc1, got %d", len(lines))
	}
}

func TestFilterByLevel(t *testing.T) {
	buf := New(10)

	buf.Append("svc1", "INFO starting")
	buf.Append("svc1", "ERROR failed")
	buf.Append("svc1", "INFO recovered")

	lines := buf.Lines("", 0, "error")
	if len(lines) != 1 {
		t.Errorf("expected 1 error line, got %d", len(lines))
	}
}

func TestLimitLines(t *testing.T) {
	buf := New(100)

	for i := 0; i < 50; i++ {
		buf.Append("svc", "line")
	}

	lines := buf.Lines("", 10, "")
	if len(lines) != 10 {
		t.Errorf("expected 10 lines, got %d", len(lines))
	}
}

func TestRingBufferOverflow(t *testing.T) {
	buf := New(3)

	buf.Append("svc", "line1")
	buf.Append("svc", "line2")
	buf.Append("svc", "line3")
	buf.Append("svc", "line4") // overwrites line1

	lines := buf.Lines("", 0, "")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines after overflow, got %d", len(lines))
	}
	if lines[0].Message != "line2" {
		t.Errorf("expected first line to be 'line2', got %q", lines[0].Message)
	}
}

func TestDetectLevel(t *testing.T) {
	tests := []struct {
		msg   string
		level string
	}{
		{"ERROR: something failed", "error"},
		{"WARN: disk space", "warn"},
		{"INFO: started", "info"},
		{"DEBUG: details", "debug"},
		{"just a message", "unknown"},
	}

	for _, tt := range tests {
		got := detectLevel(tt.msg)
		if got != tt.level {
			t.Errorf("detectLevel(%q) = %q, want %q", tt.msg, got, tt.level)
		}
	}
}
