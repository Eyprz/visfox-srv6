package orchestration

import (
	"slices"
	"strings"
	"testing"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/orchestration/plan"
	"github.com/eyprz/visfox-srv6/iel/internal/orchestration/render"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
	"github.com/eyprz/visfox-srv6/iel/internal/translation"
)

// lab is IN 1 / IN 2 -> SW (a: IN 1 main, b: IN 2) -> one bundle VVF -> decap -> OUT 1.
func lab(t *testing.T, mode string) *ssot.Resources {
	t.Helper()
	ted := domain.TED{
		Nodes: []domain.Node{
			{ID: "h1", Hostname: "Encap-1", Role: domain.RoleHeadend},
			{ID: "h2", Hostname: "Encap-2", Role: domain.RoleHeadend},
			{ID: "sw", Hostname: "Switcher-1", Role: domain.RoleVVF, VVFID: "vvf-switcher", SIDs: []string{"fd77:aa::", "fd77:ab::"},
				Inputs: []domain.InputPort{{Port: "a", Next: 0, SID: "fd77:aa::"}, {Port: "b", Next: 1, SID: "fd77:ab::"}}},
			{ID: "ctc-1", Hostname: "Converter-1", Role: domain.RoleVVF, VVFID: "vvf-color-transcode-crop", SIDs: []string{"fd77:ca::"}},
			{ID: "decap", Hostname: "Decap", Role: domain.RoleDecap, SIDs: []string{"fd77:ff::"}},
		},
		Sources: []domain.Endpoint{
			{ID: "in-1", NodeID: "h1", VideoFormat: "8k_dg", Arg: 1},
			{ID: "in-2", NodeID: "h2", VideoFormat: "8k_dg", Arg: 2},
		},
		Destinations: []domain.Endpoint{{ID: "out-1", NodeID: "decap", VideoFormat: "8k_full", Arg: 7}},
	}
	res, err := ssot.NewResources(ted, catalogVVFs(t), []ssot.Wiring{
		{Node: "Switcher-1", Mode: mode, Inputs: map[string]string{"a": "in-1", "b": "in-2"}},
	}, ssot.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// catalogVVFs are the VVF definitions of the shipped catalog.
func catalogVVFs(t *testing.T) []apitypes.VvfDefinition {
	t.Helper()
	cat, err := ssot.LoadCatalog("../../db/catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return cat.Constraints.Vvfs
}

func translator(t *testing.T) translation.Translator {
	t.Helper()
	cat, err := ssot.LoadCatalog("../../db/catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return translation.Translator{Catalog: cat}
}

func planner(t *testing.T, mode, arg string) (render.Renderer, plan.Selector) {
	res := lab(t, mode)
	return render.Renderer{Resources: res, Arg: arg}, plan.Selector{Resources: res}
}

// bundle is the color / transcode / crop chain with default params.
func bundle() []apitypes.FunctionInstance {
	return []apitypes.FunctionInstance{
		{InstanceId: "transcode-1", Type: apitypes.FunctionTypeTranscode, Params: map[string]any{}},
		{InstanceId: "crop-2", Type: apitypes.FunctionTypeCrop, Params: map[string]any{"x": 0.0, "y": 0.0}},
		{InstanceId: "color-3", Type: apitypes.FunctionTypeColor, Params: map[string]any{"r": 0.0, "g": 0.0, "b": 0.0}},
	}
}

// requestFrom translates the on-air intent IN -> OUT 1 with the chain.
func requestFrom(t *testing.T, source string, chain []apitypes.FunctionInstance) domain.SFCRequest {
	return translator(t).Translate(domain.Intent{ID: strings.ToUpper(source) + "-OUT1", SourceID: source,
		DestinationID: "out-1", Chain: chain, OnAir: true})
}

func streamFrom(t *testing.T, source string) domain.SFCRequest {
	return requestFrom(t, source, bundle())
}

func TestLegsFilterEgressArg(t *testing.T) {
	p, a := planner(t, render.SwitcherFilter, render.ArgEgress)
	s := streamFrom(t, "in-2")
	al := a.Select([]domain.SFCRequest{s}, plan.State{}, nil).SFPs["out-1"]
	legs, err := p.Legs(s, al)
	if err != nil {
		t.Fatal(err)
	}
	// One policy over the whole path; the egress SID carries the OUT's argument.
	if len(legs) != 1 || legs[0].Endpoint != "decap" || strings.Join(legs[0].Segments, ",") != "fd77:ab::,fd77:ca::,fd77:ff::7" {
		t.Fatalf("legs = %+v", legs)
	}
}

func TestLegsSwapStreamArg(t *testing.T) {
	p, a := planner(t, render.SwitcherSwap, render.ArgStream)
	s := streamFrom(t, "in-2")
	al := a.Select([]domain.SFCRequest{s}, plan.State{}, nil).SFPs["out-1"]
	legs, err := p.Legs(s, al)
	if err != nil {
		t.Fatal(err)
	}
	// The sub stream ends at the switcher; the main stream (IN 1 on input a) carries the rest. Every
	// SID carries the argument of the stream that sends it (testbed `::N`).
	if len(legs) != 2 ||
		legs[0].Source != "in-2" || legs[0].Endpoint != "sw" || strings.Join(legs[0].Segments, ",") != "fd77:ab::2" ||
		legs[1].Source != "in-1" || !legs[1].Carrier || strings.Join(legs[1].Segments, ",") != "fd77:aa::1,fd77:ca::1,fd77:ff::1" {
		t.Fatalf("legs = %+v", legs)
	}
	if got := segmentsOf(legs); got != "fd77:ab::2 | fd77:aa::1,fd77:ca::1,fd77:ff::1" {
		t.Fatalf("legs = %s", got)
	}
}

func segmentsOf(legs []render.Leg) string {
	parts := make([]string, len(legs))
	for i, l := range legs {
		parts[i] = strings.Join(l.Segments, ",")
	}
	return strings.Join(parts, " | ")
}

func opsOf(ins []domain.Instruction) []string {
	out := make([]string, len(ins))
	for i, in := range ins {
		out[i] = string(in.Op) + " " + in.Target
	}
	return out
}

// installed are the SR Policies of a stream on its SFP (what the headends hold for it).
func installed(t *testing.T, p render.Renderer, s domain.SFCRequest, al domain.SFP) []domain.SRPolicy {
	t.Helper()
	legs, err := p.Legs(s, al)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]domain.SRPolicy, len(legs))
	for i, l := range legs {
		out[i] = p.Policy(l, s.DestinationID)
	}
	return out
}

func TestTakeOrderSwap(t *testing.T) {
	p, a := planner(t, render.SwitcherSwap, render.ArgNone)
	prev, next := streamFrom(t, "in-1"), streamFrom(t, "in-2")
	prevAl := a.Select([]domain.SFCRequest{prev}, plan.State{}, nil).SFPs["out-1"]
	al := a.Select([]domain.SFCRequest{next}, plan.State{}, nil).SFPs["out-1"]
	pl, err := p.Realize("out-1", &next, al, true, nil, installed(t, p, prev, prevAl))
	if err != nil {
		t.Fatal(err)
	}
	// VVF config, the sub stream up to the switcher, the switch, then the main stream's tail.
	want := []string{"function.set_params ctc-1", "sr_policy.set h2", "switcher.select sw", "sr_policy.set h1"}
	if got := opsOf(pl.Instructions); !slices.Equal(got, want) {
		t.Fatalf("take = %v", got)
	}
	// IN 1 is the main stream: it keeps sending, nothing is released.
	if len(pl.Release) != 0 {
		t.Fatalf("release = %v", opsOf(pl.Release))
	}
}

func TestTakeOrderFilterReleasesPrevious(t *testing.T) {
	p, a := planner(t, render.SwitcherFilter, render.ArgNone)
	prev, next := streamFrom(t, "in-1"), streamFrom(t, "in-2")
	prevAl := a.Select([]domain.SFCRequest{prev}, plan.State{}, nil).SFPs["out-1"]
	al := a.Select([]domain.SFCRequest{next}, plan.State{}, nil).SFPs["out-1"]
	// The node already runs this config: no set_params.
	configured := map[string]string{"ctc-1": next.SFs[0].Config()}
	pl, err := p.Realize("out-1", &next, al, true, configured, installed(t, p, prev, prevAl))
	if err != nil {
		t.Fatal(err)
	}
	if got := opsOf(pl.Instructions); !slices.Equal(got, []string{"sr_policy.set h2", "switcher.select sw"}) {
		t.Fatalf("take = %v", got)
	}
	if got := opsOf(pl.Release); !slices.Equal(got, []string{"sr_policy.delete h1"}) {
		t.Fatalf("release = %v", got)
	}
	if pol := pl.Instructions[0].Policy; pol.Color != 101 || pol.Endpoint != "decap" {
		t.Fatalf("policy = %+v", pol)
	}
}

func TestWithArg(t *testing.T) {
	for _, c := range []struct {
		sid  string
		arg  uint64
		want string
		err  bool
	}{
		{"fd77:ff::", 0, "fd77:ff::", false},
		{"fd77:ff::", 1, "fd77:ff::1", false},
		{"fd77:ff::", 0x10000, "fd77:ff::1:0", false},
		{"fd77:ff::1", 1, "", true},
		{"10.0.0.1", 1, "", true},
	} {
		got, err := render.WithArg(c.sid, c.arg)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("WithArg(%s, %d) = %q, %v", c.sid, c.arg, got, err)
		}
	}
}

func TestSelectMakeBeforeBreak(t *testing.T) {
	res := lab(t, render.SwitcherFilter)
	// Two bundle nodes for this test.
	ted := res.TED
	ted.Nodes = append(ted.Nodes, domain.Node{ID: "ctc-2", Hostname: "Converter-2", Role: domain.RoleVVF,
		VVFID: "vvf-color-transcode-crop", SIDs: []string{"fd77:cb::"}})
	res, err := ssot.NewResources(ted, catalogVVFs(t), []ssot.Wiring{
		{Node: "Switcher-1", Inputs: map[string]string{"a": "in-1", "b": "in-2"}},
	}, ssot.Limits{VVFStreams: map[string]int{"vvf-color-transcode-crop": 1}})
	if err != nil {
		t.Fatal(err)
	}
	a := plan.Selector{Resources: res}
	onAir := streamFrom(t, "in-1")
	al := a.Select([]domain.SFCRequest{onAir}, plan.State{}, nil)
	configured := map[string]string{"ctc-1": onAir.SFs[0].Config()}
	if al.SFPs["out-1"].Nodes[0] != "ctc-1" {
		t.Fatalf("first = %v", al.SFPs["out-1"].Nodes)
	}
	// Taking another config to the same OUT avoids the node on air.
	chain := bundle()
	chain[1].Params = map[string]any{"x": 10.0, "y": 10.0}
	next := requestFrom(t, "in-2", chain)
	al = a.Select([]domain.SFCRequest{next}, plan.State{Previous: al.SFPs, Configured: configured}, map[string]bool{"out-1": true})
	if al.SFPs["out-1"].Nodes[0] != "ctc-2" {
		t.Fatalf("make-before-break = %v", al.SFPs["out-1"].Nodes)
	}
}
