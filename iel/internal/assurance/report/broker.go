// Package report is the reporting part of Intent Assurance (RFC 9315 §5.2.4, report): it tells the
// Application Layer what the IEL knows about the intents and the network (SVoT): SSE events and the
// operation log. Monitoring and compliance assessment (drift, LKG rollback) join later.
package report

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"
)

// Frame is one SSE frame: the event id (for Last-Event-ID), the event type and its JSON payload.
type Frame struct {
	ID    string
	Event string
	Data  []byte
}

// Publisher sends an SSE event (event type, JSON payload with the same `type`). It must not block.
type Publisher interface {
	Publish(eventType string, payload any)
}

// Broker broadcasts SSE events (GET /events) and keeps the latest frames so a reconnecting client can
// resume with Last-Event-ID. A client that falls behind is disconnected; it
// reconnects and resyncs with GET /matrix.
type Broker struct {
	mu       sync.Mutex
	seq      uint64
	buffer   []Frame
	subs     map[chan Frame]struct{}
	lastSent time.Time
	logger   *slog.Logger
}

const (
	brokerBuffer   = 500
	clientBuffer   = 256
	heartbeatAfter = 15 * time.Second
)

// NewBroker returns a broker without clients.
func NewBroker(logger *slog.Logger) *Broker {
	return &Broker{subs: map[chan Frame]struct{}{}, lastSent: time.Now(), logger: logger}
}

// Publish sends an event to every client. It never blocks.
func (h *Broker) Publish(eventType string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		h.logger.Error("SSE event not sent", "type", eventType, "error", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	f := Frame{ID: strconv.FormatUint(h.seq, 10), Event: eventType, Data: data}
	h.buffer = append(h.buffer, f)
	if len(h.buffer) > brokerBuffer {
		h.buffer = h.buffer[len(h.buffer)-brokerBuffer:]
	}
	h.lastSent = time.Now()
	for ch := range h.subs {
		select {
		case ch <- f:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// Subscribe registers a client. Frames after lastEventID are replayed when it is still buffered.
func (h *Broker) Subscribe(lastEventID string) (<-chan Frame, []Frame, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var replay []Frame
	if lastEventID != "" {
		for i, f := range h.buffer {
			if f.ID == lastEventID {
				replay = append(replay, h.buffer[i+1:]...)
				break
			}
		}
	}
	ch := make(chan Frame, clientBuffer)
	h.subs[ch] = struct{}{}
	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
	}
	return ch, replay, cancel
}

// RunHeartbeat sends a heartbeat when nothing was sent for 15 s, until ctx ends.
func (h *Broker) RunHeartbeat(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			h.mu.Lock()
			idle := now.Sub(h.lastSent) >= heartbeatAfter
			h.mu.Unlock()
			if idle {
				h.Publish(string(apitypes.HeartbeatEventTypeHeartbeat), apitypes.HeartbeatEvent{
					Type: apitypes.HeartbeatEventTypeHeartbeat, Timestamp: now.UTC(),
				})
			}
		}
	}
}

// Clients is the number of connected SSE clients.
func (h *Broker) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
