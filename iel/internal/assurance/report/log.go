package report

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"
)

// maxEntries is the number of log entries kept in memory (GET /logs).
const maxEntries = 1000

var levelRank = map[apitypes.LogLevel]int{
	apitypes.LogLevelDebug: 0, apitypes.LogLevelInfo: 1, apitypes.LogLevelWarn: 2, apitypes.LogLevelError: 3,
}

// KnownLevel reports whether the level is one of debug, info, warn, error.
func KnownLevel(l apitypes.LogLevel) bool {
	_, ok := levelRank[l]
	return ok
}

// Log is the operation log: every entry goes to the process log, and entries at or above the
// configured level are kept for GET /logs and published as SSE `log` events. It is safe for
// concurrent use.
type Log struct {
	mu      sync.Mutex
	logger  *slog.Logger
	pub     Publisher
	now     func() time.Time
	level   apitypes.LogLevel
	entries []apitypes.LogEntry
	seq     int
}

// NewLog returns an empty log at the level.
func NewLog(logger *slog.Logger, pub Publisher, now func() time.Time, level apitypes.LogLevel) *Log {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Log{logger: logger, pub: pub, now: now, level: level}
}

// SetLevel changes the level entries must reach to be kept.
func (l *Log) SetLevel(level apitypes.LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// Record logs an operation, optionally for a cell and with an error code.
func (l *Log) Record(level apitypes.LogLevel, message, cellID, code string) {
	attrs := []any{}
	if cellID != "" {
		attrs = append(attrs, "cell", cellID)
	}
	if code != "" {
		attrs = append(attrs, "code", code)
	}
	var sl slog.Level
	_ = sl.UnmarshalText([]byte(strings.ToUpper(string(level))))
	l.logger.Log(context.Background(), sl, message, attrs...)

	l.mu.Lock()
	defer l.mu.Unlock()
	if levelRank[level] < levelRank[l.level] {
		return
	}
	l.seq++
	e := apitypes.LogEntry{Id: fmt.Sprintf("log-%d", l.seq), Timestamp: l.now(), Level: level, Message: message}
	if cellID != "" {
		e.CellId = &cellID
	}
	if code != "" {
		e.Code = &code
	}
	l.entries = append([]apitypes.LogEntry{e}, l.entries...)
	if len(l.entries) > maxEntries {
		l.entries = l.entries[:maxEntries]
	}
	if l.pub != nil {
		l.pub.Publish(string(apitypes.LogEventTypeLog), apitypes.LogEvent{Type: apitypes.LogEventTypeLog, Log: e})
	}
}

// Entries returns the newest entries first, at or above level, optionally for one cell.
func (l *Log) Entries(level *apitypes.LogLevel, cellID string, limit int) []apitypes.LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, maxEntries)
	out := []apitypes.LogEntry{}
	for _, e := range l.entries {
		if level != nil && levelRank[e.Level] < levelRank[*level] {
			continue
		}
		if cellID != "" && (e.CellId == nil || *e.CellId != cellID) {
			continue
		}
		out = append(out, e)
		if len(out) == limit {
			break
		}
	}
	return out
}
