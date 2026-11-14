package vvf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	cfg := domain.Instruction{Op: domain.OpFunctionSetParams, Target: "caption-1"}
	if err := c.Apply(ctx, cfg); err == nil || !strings.Contains(err.Error(), "not defined yet") {
		t.Fatalf("config = %v", err)
	}
}
