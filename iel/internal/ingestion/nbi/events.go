package nbi

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/eyprz/visfox-srv6/iel/internal/assurance/report"
)

// EventSource hands out the SSE stream (internal/assurance/report Broker).
type EventSource interface {
	// Subscribe returns the live frames, the frames to replay after lastEventID, and a cancel func.
	Subscribe(lastEventID string) (<-chan report.Frame, []report.Frame, func())
}

// StreamEventsEndpoint serves streamEvents: GET /events (Server-Sent Events) until the client goes
// away.
func StreamEventsEndpoint(src EventSource) echo.HandlerFunc {
	return func(c *echo.Context) error { return stream(c, src) }
}

func stream(c *echo.Context, src EventSource) error {
	w := c.Response()
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("Connection", "keep-alive")
	hdr.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)

	frames, replay, cancel := src.Subscribe(c.Request().Header.Get("Last-Event-ID"))
	defer cancel()
	write := func(f report.Frame) error {
		if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", f.ID, f.Event, f.Data); err != nil {
			return err
		}
		return rc.Flush()
	}
	// Tell the client the stream is open (a comment line) even before the first event.
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return nil
	}
	if err := rc.Flush(); err != nil {
		return nil
	}
	for _, f := range replay {
		if err := write(f); err != nil {
			return nil
		}
	}
	ctx := c.Request().Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case f, ok := <-frames:
			if !ok {
				return nil // fell behind: the client reconnects and resyncs
			}
			if err := write(f); err != nil {
				return nil
			}
		}
	}
}
