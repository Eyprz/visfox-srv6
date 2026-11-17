package vvf

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

func TestSwitcherSelect(t *testing.T) {
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/config/stream_switch" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body)
	}))
	defer srv.Close()

	c := New(map[string]string{"sw-1": srv.URL}, srv.Client())
	ctx := context.Background()
	for _, next := range []int{2, 0} {
		in := domain.Instruction{Op: domain.OpSwitcherSelect, Target: "sw-1", Select: &domain.SwitcherSelect{Port: "c", Next: next}}
		if err := c.Apply(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0]["next"] != 2.0 || got[0]["enable"] != true || got[1]["next"] != 0.0 || got[1]["enable"] != false {
		t.Fatalf("requests = %v", got)
	}
}

func TestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := New(map[string]string{"sw-1": srv.URL, "caption-1": srv.URL}, srv.Client())
	ctx := context.Background()
	sel := domain.Instruction{Op: domain.OpSwitcherSelect, Target: "sw-1", Select: &domain.SwitcherSelect{Port: "a"}}
	if err := c.Apply(ctx, sel); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("router error = %v", err)
	}
	sel.Target = "sw-9"
	if err := c.Apply(ctx, sel); err == nil || !strings.Contains(err.Error(), "no MGMT endpoint") {
		t.Fatalf("unknown node = %v", err)
	}
	cfg := domain.Instruction{Op: domain.OpFunctionSetParams, Target: "caption-1",
		Functions: []domain.FunctionConfig{{Type: "crop", Params: map[string]any{"x": 1, "y": 2}}, {Type: "delay"}}}
	if err := c.Apply(ctx, cfg); err == nil || !strings.Contains(err.Error(), "config API of delay is not defined yet") {
		t.Fatalf("config = %v", err)
	}
	cfg.Functions = []domain.FunctionConfig{{Type: "crop", Params: map[string]any{"x": 1.5, "y": 2}}}
	if err := c.Apply(ctx, cfg); err == nil || !strings.Contains(err.Error(), "not an integer") {
		t.Fatalf("bad param = %v", err)
	}
	cfg.Functions = []domain.FunctionConfig{{Type: "color", Params: map[string]any{"r": 1, "g": 2}}}
	if err := c.Apply(ctx, cfg); err == nil || !strings.Contains(err.Error(), "b is missing") {
		t.Fatalf("missing param = %v", err)
	}
}

// A process VVF config sends the functions that have a config API, and nothing for the others.
func TestFunctionConfig(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.URL.Path+" "+strings.TrimSpace(string(b)))
	}))
	defer srv.Close()
	c := New(map[string]string{"cr-1": srv.URL + "/"}, srv.Client())
	err := c.Apply(context.Background(), domain.Instruction{Op: domain.OpFunctionSetParams, Target: "cr-1",
		Functions: []domain.FunctionConfig{
			{Type: "transcode"},
			{Type: "crop", Params: map[string]any{"x": float64(2880), "y": 1620}},
			{Type: "caption", Params: map[string]any{"text": "LIVE"}},
			{Type: "color", Params: map[string]any{"r": float64(-1024), "g": 0, "b": float64(1008)}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`/config/stream_generator_full_clipping {"name":"clip","x":2880,"y":1620}`,
		`/config/stream_converter_rgb {"b":1008,"g":0,"r":-1024}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

// Selected reads the input a switcher sends on the way D4S reads the testbed routers.
func TestSelected(t *testing.T) {
	var swap, streams string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/show/stream_swap":
			_, _ = io.WriteString(w, swap)
		case "/show/stream_switch":
			_, _ = io.WriteString(w, streams)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(map[string]string{"sw-1": srv.URL}, srv.Client())
	ctx := context.Background()
	for _, tc := range []struct {
		swap, streams string
		want          int
		err           string
	}{
		{`{"threads":[{"lcore":3,"enable":false}]}`, ``, 0, ""},
		{`{"threads":[{"lcore":3,"enable":true}]}`, `{"threads":[[{"src":"in_a","is_active":false},{"src":"in_c","is_active":true}]]}`, 3, ""},
		{`{"threads":[{"lcore":3,"enable":true}]}`, `{"threads":[]}`, 1, ""},
		{`{"threads":[{"lcore":3,"enable":true}]}`, `{"threads":[[{"src":"in_a","is_active":false}]]}`, 0, "no active stream"},
		{`{"threads":[{"lcore":3,"enable":true}]}`, `{"threads":[[{"src":"bad","is_active":true}]]}`, 0, "unknown stream src"},
		{`{"threads":[]}`, ``, 0, "no thread"},
	} {
		swap, streams = tc.swap, tc.streams
		got, err := c.Selected(ctx, "sw-1")
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s / %s: err = %v", tc.swap, tc.streams, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s / %s: next = %d, %v", tc.swap, tc.streams, got, err)
		}
	}
	if _, err := c.Selected(ctx, "sw-9"); err == nil {
		t.Error("unknown node accepted")
	}
}
