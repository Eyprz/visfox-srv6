package nbi

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	_ "modernc.org/sqlite"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/db"
	"github.com/eyprz/visfox-srv6/iel/internal/assurance/report"
	"github.com/eyprz/visfox-srv6/iel/internal/config"
	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ingestion/usecase"
	"github.com/eyprz/visfox-srv6/iel/internal/orchestration/provision"
	"github.com/eyprz/visfox-srv6/iel/internal/sbi/dryrun"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot/ted"
)

// newServer builds the whole IEL from the shipped config and seeds on a temporary database.
func newServer(t *testing.T) (*echo.Echo, *report.Broker) {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn, err := sql.Open("sqlite", "file:"+t.TempDir()+"/visfox.db?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(ctx, db.Schema); err != nil {
		t.Fatal(err)
	}
	op, err := config.LoadOperator("../../../config/operator.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tg, err := ted.Build(ctx, ted.SeedFile{Path: "../../../db/ted.yaml"}, op.SIDRule, op.Bindings(), conn)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := ssot.LoadCatalog("../../../db/catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := ssot.NewResources(tg, cat.Constraints.Vvfs, op.Wiring, op.Limits)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := ssot.LoadPresetSeed("../../../db/presets.yaml")
	if err != nil {
		t.Fatal(err)
	}
	st := ssot.NewStore(conn)
	if _, err := st.SyncPresets(ctx, seed, time.Now()); err != nil {
		t.Fatal(err)
	}
	hub := report.NewBroker(logger)
	uc, err := usecase.New(ctx, usecase.Options{
		Store: st, Resources: res, Catalog: cat, Publisher: hub, Logger: logger, Arg: op.SIDRule.Arg,
		Provisioner: provision.New(provision.Options{Drivers: map[domain.Op]provision.Driver{
			domain.OpSRPolicySet: dryrun.Client{}, domain.OpSRPolicyDelete: dryrun.Client{},
			domain.OpSwitcherSelect: dryrun.Client{}, domain.OpFunctionSetParams: dryrun.Client{},
		}}),
		DefaultConfig: apitypes.DynamicConfig{SseIntervalMs: 1000, MetricsDumpIntervalS: 30, LogLevel: apitypes.LogLevelInfo},
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(logger, time.Now().Add(-90*time.Second), uc, hub), hub
}

func call(t *testing.T, e *echo.Echo, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, BasePath+path, r)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return v
}

func TestHealthz(t *testing.T) {
	e, _ := newServer(t)
	rec := call(t, e, http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := decodeBody[apitypes.ServiceHealth](t, rec)
	if body.Status != "ok" || body.Version == "" || *body.UptimeS < 90 {
		t.Fatalf("unexpected body: %+v", body)
	}
}

// Every GET endpoint the WebUI loads answers 200 with a JSON body.
func TestSnapshotEndpoints(t *testing.T) {
	e, _ := newServer(t)
	for _, path := range []string{
		"/resources/sources", "/resources/destinations", "/resources/switchers", "/resources/pools",
		"/resources/topology", "/presets", "/presets/constraints", "/presets/preset-1", "/functions",
		"/functions/crop/schema", "/views/presets/preset-1/layout", "/matrix", "/matrix/cells/IN1-OUT1",
		"/config", "/logs?limit=200",
	} {
		rec := call(t, e, http.MethodGet, path, "")
		if rec.Code != http.StatusOK || !json.Valid(rec.Body.Bytes()) {
			t.Errorf("GET %s = %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestErrors(t *testing.T) {
	e, _ := newServer(t)
	cases := []struct {
		method, path, body string
		status             int
		code               apitypes.ErrorCode
	}{
		{http.MethodGet, "/presets/nope", "", http.StatusNotFound, apitypes.ErrorCodeRESOURCENOTFOUND},
		{http.MethodPost, "/presets", `{"name":""}`, http.StatusBadRequest, apitypes.ErrorCodeINVALIDREQUEST},
		{http.MethodPost, "/presets/validate", `{"name":"x","chain":[]}`, http.StatusOK, ""},
		{http.MethodPost, "/matrix/cells/IN1-OUT1/take", "", http.StatusConflict, apitypes.ErrorCodeINVALIDCELLSTATE},
		{http.MethodPut, "/matrix/cells/IN1-OUT1/preset", `{`, http.StatusBadRequest, apitypes.ErrorCodeINVALIDREQUEST},
		{http.MethodPost, "/functions/color/validate", `{"params":{"r":1000}}`, http.StatusOK, ""},
		{http.MethodPatch, "/config", `{}`, http.StatusBadRequest, apitypes.ErrorCodeINVALIDREQUEST},
		{http.MethodGet, "/logs?level=loud", "", http.StatusBadRequest, apitypes.ErrorCodeINVALIDREQUEST},
	}
	for _, tc := range cases {
		rec := call(t, e, tc.method, tc.path, tc.body)
		if rec.Code != tc.status {
			t.Errorf("%s %s = %d %s, want %d", tc.method, tc.path, rec.Code, rec.Body.String(), tc.status)
			continue
		}
		if tc.code != "" {
			if got := decodeBody[apitypes.ErrorResponse](t, rec).Code; got != tc.code {
				t.Errorf("%s %s code = %s, want %s", tc.method, tc.path, got, tc.code)
			}
		}
	}
}

func TestAssignAndTake(t *testing.T) {
	e, _ := newServer(t)
	rec := call(t, e, http.MethodPut, "/matrix/cells/IN2-OUT1/preset", `{"preset_id":"preset-2"}`)
	if rec.Code != http.StatusOK || decodeBody[apitypes.MatrixCell](t, rec).ExecutionState != apitypes.ExecutionStateStandby {
		t.Fatalf("assign = %d %s", rec.Code, rec.Body.String())
	}
	rec = call(t, e, http.MethodPost, "/matrix/cells/IN2-OUT1/take", "")
	if rec.Code != http.StatusOK || decodeBody[apitypes.TakeResponse](t, rec).ActivatedCell != "IN2-OUT1" {
		t.Fatalf("take = %d %s", rec.Code, rec.Body.String())
	}
}

// The shipped IEL serves the predefined presets: preset writes answer 501 NOT_IMPLEMENTED.
func TestPresetWritesNotImplemented(t *testing.T) {
	e, _ := newServer(t)
	body := `{"name":"Spare","chain":[{"instance_id":"a","type":"color","params":{"r":0,"g":0,"b":0}}]}`
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/presets", body},
		{http.MethodPut, "/presets/preset-1", body},
		{http.MethodDelete, "/presets/preset-1", ""},
	} {
		rec := call(t, e, c.method, c.path, c.body)
		if rec.Code != http.StatusNotImplemented ||
			decodeBody[apitypes.ErrorResponse](t, rec).Code != apitypes.ErrorCodeNOTIMPLEMENTED {
			t.Fatalf("%s %s = %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

type frame struct{ id, event, data string }

// readFrames reads SSE frames from the stream into a channel.
func readFrames(body io.Reader) <-chan frame {
	out := make(chan frame, 64)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(body)
		var f frame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if f.event != "" {
					out <- f
				}
				f = frame{}
			case strings.HasPrefix(line, "id: "):
				f.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				f.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				f.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return out
}

func waitFor(t *testing.T, frames <-chan frame, event string) frame {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("stream closed before %s", event)
			}
			if f.event == event {
				return f
			}
		case <-timeout:
			t.Fatalf("no %s event", event)
		}
	}
}

func openStream(ctx context.Context, t *testing.T, url, lastID string) <-chan frame {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+BasePath+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	return readFrames(resp.Body)
}

func TestEvents(t *testing.T) {
	e, hub := newServer(t)
	srv := httptest.NewServer(e)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The response headers arrive after the hub registered the client.
	frames := openStream(ctx, t, srv.URL, "")
	if subscribed := hub.Clients(); subscribed != 1 {
		t.Fatalf("subscribers = %d", hub.Clients())
	}
	if rec := call(t, e, http.MethodPut, "/matrix/cells/IN1-OUT1/preset", `{"preset_id":"preset-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("assign = %d", rec.Code)
	}
	// The operation log comes first, then the changed cell.
	logFrame := waitFor(t, frames, "log")
	f := waitFor(t, frames, "matrix_update")
	var ev apitypes.MatrixUpdateEvent
	if err := json.Unmarshal([]byte(f.data), &ev); err != nil || ev.Cell.CellId != "IN1-OUT1" || ev.Type != "matrix_update" {
		t.Fatalf("event = %s (%v)", f.data, err)
	}

	// A reconnecting client gets the frames after its Last-Event-ID.
	replayed := openStream(ctx, t, srv.URL, logFrame.id)
	if got := waitFor(t, replayed, "matrix_update"); got.id != f.id {
		t.Fatalf("replayed %s, want %s", got.id, f.id)
	}
}
