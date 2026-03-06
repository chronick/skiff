package logbuf

import (
	"strings"
	"sync"
	"time"
)

// LogEntry represents a single log line.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	Level    string    `json:"level"` // info | warn | error | unknown
	Message  string    `json:"message"`
}

// LogBuffer is a thread-safe ring buffer for log entries.
type LogBuffer struct {
	mu       sync.Mutex
	entries  []LogEntry
	maxLines int
	pos      int
	full     bool
}

// New creates a LogBuffer with the given capacity.
func New(maxLines int) *LogBuffer {
	if maxLines <= 0 {
		maxLines = 500
	}
	return &LogBuffer{
		entries:  make([]LogEntry, maxLines),
		maxLines: maxLines,
	}
}

// Append adds a log entry to the buffer.
func (b *LogBuffer) Append(source, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.entries[b.pos] = LogEntry{
		Timestamp: time.Now(),
		Source:    source,
		Level:    detectLevel(message),
		Message:  message,
	}
	b.pos = (b.pos + 1) % b.maxLines
	if b.pos == 0 {
		b.full = true
	}
}

// AppendEntry adds a pre-built log entry.
func (b *LogBuffer) AppendEntry(entry LogEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.entries[b.pos] = entry
	b.pos = (b.pos + 1) % b.maxLines
	if b.pos == 0 {
		b.full = true
	}
}

// Lines returns the last n entries for a given source, optionally filtered by level.
func (b *LogBuffer) Lines(source string, n int, level string) []LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	var result []LogEntry
	count := b.maxLines
	if !b.full {
		count = b.pos
	}

	start := 0
	if b.full {
		start = b.pos
	}

	for i := 0; i < count; i++ {
		idx := (start + i) % b.maxLines
		e := b.entries[idx]
		if source != "" && e.Source != source {
			continue
		}
		if level != "" && e.Level != level {
			continue
		}
		result = append(result, e)
	}

	if n > 0 && len(result) > n {
		result = result[len(result)-n:]
	}
	return result
}

func detectLevel(msg string) string {
	upper := strings.ToUpper(msg)
	if len(upper) > 100 {
		upper = upper[:100]
	}
	switch {
	case strings.Contains(upper, "ERROR"):
		return "error"
	case strings.Contains(upper, "WARN"):
		return "warn"
	case strings.Contains(upper, "INFO"):
		return "info"
	case strings.Contains(upper, "DEBUG"):
		return "debug"
	default:
		return "unknown"
	}
}
