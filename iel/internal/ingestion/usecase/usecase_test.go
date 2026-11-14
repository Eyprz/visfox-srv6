package usecase_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/db"
	"github.com/eyprz/visfox-srv6/iel/internal/config"
	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ingestion/usecase"
	"github.com/eyprz/visfox-srv6/iel/internal/orchestration/provision"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot/ted"
)

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) Publish(eventType string, _ any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, eventType)
}

func (r *recorder) count(eventType string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e == eventType {
			n++
		}
	}
	return n
}

// southbound records the instructions the use cases provision and fails on demand.
type southbound struct {
	mu   sync.Mutex
	ins  []domain.Instruction
	fail func(domain.Instruction) error
}

func (o *southbound) Provision(_ context.Context, ins []domain.Instruction) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, in := range ins {
		if o.fail != nil {
			if err := o.fail(in); err != nil {
				return &provision.Error{Instruction: in, Driver: "test", Err: err, Applied: ins[:i]}
			}
		}
		o.ins = append(o.ins, in)
	}
	return nil
}

// take returns and forgets the recorded instructions.
func (o *southbound) take() []domain.Instruction {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := o.ins
	o.ins = nil
	return out
}

// network is the SR Policies a fake PolaPCE reports (Intent Assurance monitoring).
type network struct{ policies []domain.SRPolicy }

func (n *network) SRPolicies(context.Context) ([]domain.SRPolicy, error) { return n.policies, nil }

type fixture struct {
	// mon, when set, is the monitor the next open uses.
	mon  *network
	db   *sql.DB
	pub  *recorder
	orch *southbound
	uc   *usecase.UseCases
	// predefined serves the seed presets only (no preset editing).
	predefined bool
	seedPath   string
	// limits, when set, changes the operator config's limits (e.g. cold standby).
	limits func(*ssot.Limits)
	// network selects the shipped network ("" testbed, "rich": config/operator-rich.yaml, db/ted-rich.yaml).
	network string
}

// newFixture builds the use cases from the shipped operator config, TED seed, catalog and presets,
// with preset editing (stage C).
func newFixture(t *testing.T) *fixture { return newFixtureWith(t, false, "../../../db/presets.yaml") }

// newColdFixture is newFixture in cold standby: STANDBY holds only VVF nodes, the Take sets up the
// whole path (an environment whose switchers need no feed).
func newColdFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{limits: func(l *ssot.Limits) { l.StandbyFeed = ssot.StandbyFeedNone }}
	return f.init(t, false, "../../../db/presets.yaml")
}

func newFixtureWith(t *testing.T, predefined bool, seedPath string) *fixture {
	t.Helper()
	return (&fixture{}).init(t, predefined, seedPath)
}

func (f *fixture) init(t *testing.T, predefined bool, seedPath string) *fixture {
	t.Helper()
	ctx := context.Background()
	conn, err := sql.Open("sqlite", "file:"+t.TempDir()+"/visfox.db?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(ctx, db.Schema); err != nil {
		t.Fatal(err)
	}
	f.db, f.pub, f.orch, f.predefined, f.seedPath = conn, &recorder{}, &southbound{}, predefined, seedPath
	f.uc = f.open(t)
	return f
}

// open (re)starts the IEL use cases on the fixture's database.
func (f *fixture) open(t *testing.T) *usecase.UseCases {
	t.Helper()
	ctx := context.Background()
	suffix := ""
	if f.network != "" {
		suffix = "-" + f.network
	}
	op, err := config.LoadOperator("../../../config/operator" + suffix + ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	tg, err := ted.Build(ctx, ted.SeedFile{Path: "../../../db/ted" + suffix + ".yaml"}, op.SIDRule, op.Bindings(), f.db)
	if err != nil {
		t.Fatal(err)
	}
	if f.limits != nil {
		f.limits(&op.Limits)
	}
	cat, err := ssot.LoadCatalog("../../../db/catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := ssot.NewResources(tg, cat.Constraints.Vvfs, op.Wiring, op.Limits)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := ssot.LoadPresetSeed(f.seedPath)
	if err != nil {
		t.Fatal(err)
	}
	st := ssot.NewStore(f.db)
	if f.predefined {
		_, err = st.SyncPresets(ctx, seed, time.Now())
	} else {
		_, err = st.SeedPresets(ctx, seed, time.Now())
	}
	if err != nil {
		t.Fatal(err)
	}
	var mon usecase.Options
	if f.mon != nil {
		mon.Monitor = f.mon
	}
	uc, err := usecase.New(ctx, usecase.Options{
		Store: st, Resources: res, Catalog: cat, Publisher: f.pub, PresetEditing: !f.predefined,
		Provisioner: f.orch, Monitor: mon.Monitor, Arg: op.SIDRule.Arg,
		DefaultConfig: apitypes.DynamicConfig{SseIntervalMs: 1000, MetricsDumpIntervalS: 30, LogLevel: apitypes.LogLevelInfo},
	})
	if err != nil {
		t.Fatal(err)
	}
	return uc
}

func code(t *testing.T, err error) apitypes.ErrorCode {
	t.Helper()
	var e *domain.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected a domain error, got %v", err)
	}
	return e.Code
}

func state(t *testing.T, uc *usecase.UseCases, cellID string) apitypes.ExecutionState {
	t.Helper()
	c, err := uc.GetMatrixCell.Run(cellID)
	if err != nil {
		t.Fatal(err)
	}
	return c.ExecutionState
}

func TestInitialMatrix(t *testing.T) {
	f := newFixture(t)
	m := f.uc.GetMatrixState.Run()
	if len(m.Cells) != 16 {
		t.Fatalf("cells = %d, want 4 x 4", len(m.Cells))
	}
	if m.Cells[0].CellId != "IN1-OUT1" || m.Cells[0].ExecutionState != apitypes.ExecutionStateOff {
		t.Fatalf("first cell = %+v", m.Cells[0])
	}
	if got := len(f.uc.GetPresets.Run()); got != 3 {
		t.Fatalf("seeded presets = %d", got)
	}
}

func TestAssignTakeStandbyClear(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	c, err := f.uc.ApplyPresetToCell.Run(ctx, "IN1-OUT1", "preset-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.ExecutionState != apitypes.ExecutionStateStandby || *c.PresetName != "Caption + Crop Center" || len(*c.LiveChain) != 4 {
		t.Fatalf("assigned cell = %+v", c)
	}
	if _, err := f.uc.ApplyPresetToCell.Run(ctx, "IN2-OUT1", "preset-3", nil); err != nil {
		t.Fatal(err)
	}

	r, err := f.uc.TakeCell.Run(ctx, "IN1-OUT1")
	if err != nil || r.DeactivatedCell != nil {
		t.Fatalf("take = %+v, %v", r, err)
	}
	// Take within the OUT: the previous on-air cell steps down.
	r, err = f.uc.TakeCell.Run(ctx, "IN2-OUT1")
	if err != nil || r.DeactivatedCell == nil || *r.DeactivatedCell != "IN1-OUT1" {
		t.Fatalf("take = %+v, %v", r, err)
	}
	if state(t, f.uc, "IN1-OUT1") != apitypes.ExecutionStateStandby {
		t.Fatal("IN1-OUT1 should be standby")
	}
	if _, err := f.uc.TakeCell.Run(ctx, "IN2-OUT1"); code(t, err) != apitypes.ErrorCodeINVALIDCELLSTATE {
		t.Fatal("taking an active cell must fail")
	}

	if _, err := f.uc.StandbyCell.Run(ctx, "IN2-OUT1"); err != nil {
		t.Fatal(err)
	}
	c, err = f.uc.ClearCellPreset.Run(ctx, "IN2-OUT1")
	if err != nil || c.ExecutionState != apitypes.ExecutionStateOff || c.PresetId != nil {
		t.Fatalf("cleared = %+v, %v", c, err)
	}
	if f.pub.count("matrix_update") == 0 || f.pub.count("log") == 0 {
		t.Fatalf("events = %v", f.pub.events)
	}
}

// Cold standby: one IN is on air to one OUT at a time (source_unicast), checked at the Take.
func TestSourceUnicast(t *testing.T) {
	f := newColdFixture(t)
	ctx := context.Background()
	for _, id := range []string{"IN1-OUT1", "IN1-OUT2"} {
		if _, err := f.uc.ApplyPresetToCell.Run(ctx, id, "preset-1", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.uc.TakeCell.Run(ctx, "IN1-OUT1"); err != nil {
		t.Fatal(err)
	}
	c, _ := f.uc.GetMatrixCell.Run("IN1-OUT2")
	if len(*c.TakeIssues) != 1 || *(*c.TakeIssues)[0].ResourceKind != ssot.PoolSourceUnicast {
		t.Fatalf("take issues = %+v", c.TakeIssues)
	}
	if _, err := f.uc.TakeCell.Run(ctx, "IN1-OUT2"); code(t, err) != apitypes.ErrorCodeRESOURCEEXHAUSTED {
		t.Fatal("second OUT for the same IN must be refused")
	}
	pools := f.uc.GetResourcePools.Run()
	for _, p := range pools {
		if p.Id == "source_unicast:in-1" && len(p.UsedBy) != 1 {
			t.Fatalf("pool = %+v", p)
		}
	}
}

func TestPresetLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	chain := []apitypes.FunctionInstance{
		{InstanceId: "sf-transcode-1", Type: apitypes.FunctionTypeTranscode, Params: map[string]any{}},
		{InstanceId: "sf-crop-1", Type: apitypes.FunctionTypeCrop, Params: map[string]any{"x": 0.0, "y": 0.0}},
		{InstanceId: "sf-color-1", Type: apitypes.FunctionTypeColor, Params: map[string]any{"r": 0.0, "g": 0.0, "b": 0.0}},
	}
	p, err := f.uc.CreatePreset.Run(ctx, apitypes.PresetInput{Name: "Plain", Chain: chain})
	if err != nil || p.Id != "preset-4" {
		t.Fatalf("created = %+v, %v", p, err)
	}
	if _, err := f.uc.ApplyPresetToCell.Run(ctx, "IN3-OUT3", p.Id, nil); err != nil {
		t.Fatal(err)
	}
	// In use: params may change, the structure may not.
	if _, err := f.uc.UpdatePreset.Run(ctx, p.Id, apitypes.PresetInput{Name: "Plain", Chain: chain[:1]}); code(t, err) != apitypes.ErrorCodePRESETINUSE {
		t.Fatal("structure change of a used preset must fail")
	}
	r, err := f.uc.PatchPresetFunctionParams.Run(ctx, p.Id, "sf-color-1", map[string]any{"r": 12.0})
	if err != nil || len(r.AppliedCellIds) != 1 || r.Instance.Params["r"] != 12.0 {
		t.Fatalf("patch = %+v, %v", r, err)
	}
	if _, err := f.uc.PatchPresetFunctionParams.Run(ctx, p.Id, "sf-color-1", map[string]any{"r": 999.0}); code(t, err) != apitypes.ErrorCodeINVALIDPARAMS {
		t.Fatal("out-of-range params must fail")
	}
	if err := f.uc.DeletePreset.Run(ctx, p.Id); code(t, err) != apitypes.ErrorCodePRESETINUSE {
		t.Fatal("deleting a used preset must fail")
	}

	// Everything survives a restart (SQLite), and the seed is not inserted again.
	uc := f.open(t)
	got, err := uc.GetPresetByID.Run(p.Id)
	if err != nil || got.Chain[2].Params["r"] != 12.0 || len(*got.InUseCellIds) != 1 {
		t.Fatalf("after restart = %+v, %v", got, err)
	}
	if n := len(uc.GetPresets.Run()); n != 4 {
		t.Fatalf("presets after restart = %d", n)
	}
	if _, err := uc.ClearCellPreset.Run(ctx, "IN3-OUT3"); err != nil {
		t.Fatal(err)
	}
	if err := uc.DeletePreset.Run(ctx, p.Id); err != nil {
		t.Fatal(err)
	}
}

func TestCreatePresetRejectsInvalidChain(t *testing.T) {
	f := newFixture(t)
	// A lone color is an incomplete Color / Transcode / Crop VVF and outputs 8K DG.
	_, err := f.uc.CreatePreset.Run(context.Background(), apitypes.PresetInput{Name: "Bad", Chain: []apitypes.FunctionInstance{
		{InstanceId: "sf-color-1", Type: apitypes.FunctionTypeColor, Params: map[string]any{"r": 0.0, "g": 0.0, "b": 0.0}},
	}})
	if code(t, err) != apitypes.ErrorCodeINVALIDCHAIN {
		t.Fatal("expected INVALID_CHAIN")
	}
}

func TestPathAndTopology(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.uc.GetCellPath.Run("IN1-OUT1"); code(t, err) != apitypes.ErrorCodeINVALIDCELLSTATE {
		t.Fatal("an OFF cell has no path")
	}
	if _, err := f.uc.ApplyPresetToCell.Run(ctx, "IN1-OUT1", "preset-1", nil); err != nil {
		t.Fatal(err)
	}
	p, err := f.uc.GetCellPath.Run("IN1-OUT1")
	if err != nil {
		t.Fatal(err)
	}
	// A standby cell shows the way it would take: headend, SW1 (input a), caption, the first free
	// color / transcode / crop node, egress, and the Segment List with the OUT's egress argument.
	if len(p.Nodes) != 5 || p.Nodes[0].Role != apitypes.PathNodeRoleHeadend || p.Nodes[4].Role != apitypes.PathNodeRoleEgress {
		t.Fatalf("path = %+v", p.Nodes)
	}
	if p.Nodes[1].Switcher == nil || p.Nodes[1].Switcher.InputPort != "a" || *p.Nodes[1].Sid != "fd77:aa::" {
		t.Fatalf("switcher = %+v", p.Nodes[1])
	}
	// The pinned caption runs on the node the preset names, with the operator's display name.
	if p.Nodes[2].NodeId != "caption-fd77-ba" || p.Nodes[2].Label != "Caption: Logo" || *p.Nodes[2].Sid != "fd77:ba::" {
		t.Fatalf("caption node = %+v", p.Nodes[2])
	}
	if p.Nodes[3].NodeId != "color-transcode-crop-fd77-ca" || (*p.Nodes[3].InstanceIds)[0] != "sf-transcode-1" {
		t.Fatalf("bundle node = %+v", p.Nodes[3])
	}
	if got := strings.Join(*p.SegmentList, ","); got != "fd77:aa::,fd77:ba::,fd77:ca::,fd77:ff::1" {
		t.Fatalf("segment list = %s", got)
	}
	if _, err := f.uc.TakeCell.Run(ctx, "IN1-OUT1"); err != nil {
		t.Fatal(err)
	}
	topo := f.uc.GetTopology.Run()
	// in-1 -> SW1 -> caption -> bundle -> out-1
	if len(topo.Nodes) != 4+6+2+4 || len(topo.Links) != 4 {
		t.Fatalf("topology nodes=%d links=%d", len(topo.Nodes), len(topo.Links))
	}
	sw := f.uc.GetSwitchers.Run()
	if len(sw) != 2 || len(sw[0].Inputs) != 5 || sw[0].Inputs[4].From != sw[1].Id {
		t.Fatalf("switchers = %+v", sw)
	}
}

func TestConstraintsListPinnedNodes(t *testing.T) {
	f := newFixture(t)
	vvfs := f.uc.GetPresetConstraints.Run().Vvfs
	if vvfs[0].Allocation != apitypes.VvfDefinitionAllocationPinned || len(*vvfs[0].Nodes) != 3 ||
		(*vvfs[0].Nodes)[2].Label != "Caption: Sponsor" {
		t.Fatalf("caption VVF = %+v", vvfs[0])
	}
	if vvfs[1].Allocation != apitypes.VvfDefinitionAllocationDynamic || len(*vvfs[1].Nodes) != 0 {
		t.Fatalf("bundle VVF = %+v", vvfs[1])
	}
	// Moving a used preset's caption to another node is a structure change.
	ctx := context.Background()
	if _, err := f.uc.ApplyPresetToCell.Run(ctx, "IN1-OUT1", "preset-1", nil); err != nil {
		t.Fatal(err)
	}
	p, _ := f.uc.GetPresetByID.Run("preset-1")
	p.Chain[0].VvfNodeId = ptr("caption-fd77-bc")
	_, err := f.uc.UpdatePreset.Run(ctx, "preset-1", apitypes.PresetInput{Name: p.Name, Chain: p.Chain})
	if code(t, err) != apitypes.ErrorCodePRESETINUSE {
		t.Fatal("moving a used preset to another pinned node must fail")
	}
}

func ptr[T any](v T) *T { return &v }

func TestPatchConfig(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.uc.UpdateConfig.Run(ctx, apitypes.DynamicConfigPatch{}); code(t, err) != apitypes.ErrorCodeINVALIDREQUEST {
		t.Fatal("an empty patch must fail")
	}
	bad := 50
	if _, err := f.uc.UpdateConfig.Run(ctx, apitypes.DynamicConfigPatch{SseIntervalMs: &bad}); code(t, err) != apitypes.ErrorCodeINVALIDPARAMS {
		t.Fatal("a too short interval must fail")
	}
	warn := apitypes.LogLevelWarn
	cfg, err := f.uc.UpdateConfig.Run(ctx, apitypes.DynamicConfigPatch{LogLevel: &warn})
	if err != nil || cfg.Dynamic.LogLevel != warn {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	// Info logs are dropped at warn.
	before := len(f.uc.GetLogs.Run(nil, "", 1000))
	if _, err := f.uc.ApplyPresetToCell.Run(ctx, "IN1-OUT1", "preset-1", nil); err != nil {
		t.Fatal(err)
	}
	if after := len(f.uc.GetLogs.Run(nil, "", 1000)); after != before {
		t.Fatalf("logs grew from %d to %d at warn", before, after)
	}
	if got := f.open(t).GetConfig.Run().Dynamic.LogLevel; got != warn {
		t.Fatalf("log level after restart = %s", got)
	}
}

func TestPredefinedPresets(t *testing.T) {
	ctx := context.Background()
	f := newFixtureWith(t, true, "../../../db/presets.yaml")
	p, err := f.uc.GetPresetByID.Run("preset-1")
	if err != nil {
		t.Fatal(err)
	}

	// Writes answer 501; parameter changes and assignment work.
	in := apitypes.PresetInput{Name: p.Name, Chain: p.Chain}
	if _, err := f.uc.CreatePreset.Run(ctx, in); code(t, err) != apitypes.ErrorCodeNOTIMPLEMENTED {
		t.Fatalf("create = %v", err)
	}
	if _, err := f.uc.UpdatePreset.Run(ctx, p.Id, in); code(t, err) != apitypes.ErrorCodeNOTIMPLEMENTED {
		t.Fatalf("update = %v", err)
	}
	if err := f.uc.DeletePreset.Run(ctx, p.Id); code(t, err) != apitypes.ErrorCodeNOTIMPLEMENTED {
		t.Fatalf("delete = %v", err)
	}
	if _, err := f.uc.ApplyPresetToCell.Run(ctx, "IN1-OUT1", p.Id, nil); err != nil {
		t.Fatal(err)
	}
	fn := p.Chain[0]
	if _, err := f.uc.PatchPresetFunctionParams.Run(ctx, p.Id, fn.InstanceId, fn.Params); err != nil {
		t.Fatal(err)
	}

	// The seed file replaces the stored presets at the next start: a preset that left the file is
	// deleted and its cells turn OFF.
	seed, err := ssot.LoadPresetSeed("../../../db/presets.yaml")
	if err != nil {
		t.Fatal(err)
	}
	seed.Presets = slices.DeleteFunc(seed.Presets, func(x apitypes.Preset) bool { return x.Id == p.Id })
	seed.Layouts = slices.DeleteFunc(seed.Layouts, func(x apitypes.PresetLayout) bool { return x.PresetId == p.Id })
	path := filepath.Join(t.TempDir(), "presets.yaml")
	raw, err := json.Marshal(seed) // JSON is YAML
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	f.seedPath = path
	uc := f.open(t)
	if _, err := uc.GetPresetByID.Run(p.Id); code(t, err) != apitypes.ErrorCodeRESOURCENOTFOUND {
		t.Fatalf("removed preset = %v", err)
	}
	if got := state(t, uc, "IN1-OUT1"); got != apitypes.ExecutionStateOff {
		t.Fatalf("cell of a removed preset = %s", got)
	}
	if n := len(uc.GetPresets.Run()); n != len(seed.Presets) {
		t.Fatalf("presets = %d, want %d", n, len(seed.Presets))
	}
}

// ops lists the instructions as "op target".
func ops(ins []domain.Instruction) []string {
	out := make([]string, len(ins))
	for i, in := range ins {
		out[i] = string(in.Op) + " " + in.Target
	}
	return out
}

func TestTakeRunsThePlan(t *testing.T) {
	ctx := context.Background()
	f := newColdFixture(t)
	for _, a := range [][2]string{{"IN1-OUT1", "preset-1"}, {"IN2-OUT1", "preset-2"}} {
		if _, err := f.uc.ApplyPresetToCell.Run(ctx, a[0], a[1], nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.orch.take(); len(got) != 0 {
		t.Fatalf("STANDBY sent %v (nodes are only reserved)", ops(got))
	}

	// VVF configs first, then the path, then the switch (filter switchers).
	if _, err := f.uc.TakeCell.Run(ctx, "IN1-OUT1"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"function.set_params caption-fd77-ba",
		"function.set_params color-transcode-crop-fd77-ca",
		"sr_policy.set headend-100g-8kdg-encap-a",
		"switcher.select sw-fd77-a",
	}
	got := f.orch.take()
	if !slices.Equal(ops(got), want) {
		t.Fatalf("take = %v", ops(got))
	}
	if pol := got[2].Policy; pol.Color != 101 || strings.Join(pol.SegmentList, ",") != "fd77:aa::,fd77:ba::,fd77:ca::,fd77:ff::1" {
		t.Fatalf("policy = %+v", pol)
	}

	// Make-before-break: IN 2 takes another bundle node than the one on air; the previous stream's
	// policy is withdrawn after the switch.
	if _, err := f.uc.TakeCell.Run(ctx, "IN2-OUT1"); err != nil {
		t.Fatal(err)
	}
	got = f.orch.take()
	if !slices.Equal(ops(got), []string{
		"function.set_params caption-fd77-bb",
		"function.set_params color-transcode-crop-fd77-cb",
		"sr_policy.set headend-100g-8kdg-encap-b",
		"switcher.select sw-fd77-a",
		"sr_policy.delete headend-100g-8kdg-encap-a",
	}) {
		t.Fatalf("second take = %v", ops(got))
	}
	pools := f.uc.GetResourcePools.Run()
	i := slices.IndexFunc(pools, func(p apitypes.ResourcePool) bool { return p.Id == "vvf_node:color-transcode-crop-fd77-cb" })
	if len(pools[i].UsedBy) != 1 || !pools[i].UsedBy[0].OnAir {
		t.Fatalf("pool = %+v", pools[i])
	}

	// A southbound failure leaves the state as it was.
	f.orch.fail = func(in domain.Instruction) error {
		if in.Op == domain.OpSRPolicySet {
			return errors.New("PCEP session down")
		}
		return nil
	}
	_, err := f.uc.TakeCell.Run(ctx, "IN1-OUT1")
	if code(t, err) != apitypes.ErrorCodeROUTERERROR || state(t, f.uc, "IN2-OUT1") != apitypes.ExecutionStateActive {
		t.Fatalf("failed take = %v, IN2-OUT1 %s", err, state(t, f.uc, "IN2-OUT1"))
	}
	f.orch.fail = nil

	// Standby withdraws the stream's policy.
	f.orch.take()
	if _, err := f.uc.StandbyCell.Run(ctx, "IN2-OUT1"); err != nil {
		t.Fatal(err)
	}
	if got := ops(f.orch.take()); !slices.Equal(got, []string{"sr_policy.delete headend-100g-8kdg-encap-b"}) {
		t.Fatalf("standby = %v", got)
	}
}

func TestDynamicVVFAllocation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	// Two OUTs with different bundle configs on air at once use two bundle nodes.
	for _, a := range [][2]string{{"IN1-OUT1", "preset-2"}, {"IN2-OUT2", "preset-3"}} {
		if _, err := f.uc.ApplyPresetToCell.Run(ctx, a[0], a[1], nil); err != nil {
			t.Fatal(err)
		}
	}
	nodes := map[string]bool{}
	for _, c := range []string{"IN1-OUT1", "IN2-OUT2"} {
		p, err := f.uc.GetCellPath.Run(c)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range p.Nodes {
			if n.VvfId != nil && *n.VvfId == "vvf-color-transcode-crop" {
				nodes[n.NodeId] = true
			}
		}
	}
	if len(nodes) != 2 {
		t.Fatalf("bundle nodes = %v", nodes)
	}
}

// A failed Take rolls the OUT back to its LKG: the on-air stream's path and switch are provisioned
// again, the policy the failed Take left is withdrawn, and the error carries the rollback.
func TestTakeFailureRollsBackToLKG(t *testing.T) {
	ctx := context.Background()
	f := newColdFixture(t)
	for _, a := range [][2]string{{"IN1-OUT1", "preset-1"}, {"IN2-OUT1", "preset-2"}} {
		if _, err := f.uc.ApplyPresetToCell.Run(ctx, a[0], a[1], nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.uc.TakeCell.Run(ctx, "IN1-OUT1"); err != nil {
		t.Fatal(err)
	}
	f.orch.take()

	// The switch of the next Take fails once.
	failed := false
	f.orch.fail = func(in domain.Instruction) error {
		if in.Op == domain.OpSwitcherSelect && !failed {
			failed = true
			return errors.New("switcher busy")
		}
		return nil
	}
	_, err := f.uc.TakeCell.Run(ctx, "IN2-OUT1")
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != apitypes.ErrorCodeROUTERERROR || len(de.Rollbacks) != 1 ||
		de.Rollbacks[0].Result != apitypes.RollbackInfoResultSucceeded || de.Rollbacks[0].CellId != "IN2-OUT1" {
		t.Fatalf("err = %+v", err)
	}
	got := ops(f.orch.take())
	want := []string{
		// the attempt (up to the failed switch)
		"function.set_params caption-fd77-bb",
		"function.set_params color-transcode-crop-fd77-cb",
		"sr_policy.set headend-100g-8kdg-encap-b",
		// the rollback: IN 1 on its path again, IN 2's policy withdrawn
		"sr_policy.set headend-100g-8kdg-encap-a",
		"switcher.select sw-fd77-a",
		"sr_policy.delete headend-100g-8kdg-encap-b",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("instructions = %v", got)
	}
	if state(t, f.uc, "IN1-OUT1") != apitypes.ExecutionStateActive || state(t, f.uc, "IN2-OUT1") != apitypes.ExecutionStateStandby {
		t.Fatal("the state must stay as before the Take")
	}
	c, _ := f.uc.GetMatrixCell.Run("IN2-OUT1")
	if c.LastRollback == nil || c.LastRollback.To == nil || *c.HealthState != apitypes.HealthStateHealthy || f.pub.count("rollback") != 2 {
		t.Fatalf("cell = %+v, rollback events = %d", c, f.pub.count("rollback"))
	}
	// The SSoT holds only IN 1's policy.
	r, err := ssot.NewStore(f.db).Realization(ctx)
	if err != nil || len(r.Policies) != 1 || r.Policies[0].Headend != "headend-100g-8kdg-encap-a" {
		t.Fatalf("realization = %+v, %v", r.Policies, err)
	}
}

// What orchestration selected and provisioned is SSoT: a restart keeps the paths on air.
func TestRealizationSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for _, a := range [][2]string{{"IN1-OUT1", "preset-1"}, {"IN2-OUT2", "preset-3"}} {
		if _, err := f.uc.ApplyPresetToCell.Run(ctx, a[0], a[1], nil); err != nil {
			t.Fatal(err)
		}
		if _, err := f.uc.TakeCell.Run(ctx, a[0]); err != nil {
			t.Fatal(err)
		}
	}
	paths := map[string]string{}
	for _, id := range []string{"IN1-OUT1", "IN2-OUT2"} {
		p, err := f.uc.GetCellPath.Run(id)
		if err != nil {
			t.Fatal(err)
		}
		paths[id] = strings.Join(*p.SegmentList, ",")
	}
	f.orch.take()
	uc := f.open(t)
	for id, want := range paths {
		p, err := uc.GetCellPath.Run(id)
		if err != nil || strings.Join(*p.SegmentList, ",") != want {
			t.Fatalf("%s after restart = %v, want %s (%v)", id, p.SegmentList, want, err)
		}
	}
	// The configs the nodes run survive too: taking another config to OUT 1 avoids the bundle node
	// on air (make-before-break) instead of reconfiguring it.
	if _, err := uc.ApplyPresetToCell.Run(ctx, "IN3-OUT1", "preset-3", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.TakeCell.Run(ctx, "IN3-OUT1"); err != nil {
		t.Fatal(err)
	}
	for _, op := range ops(f.orch.take()) {
		if op == "function.set_params color-transcode-crop-fd77-ca" {
			t.Fatalf("the on-air node was reconfigured after the restart: %s", op)
		}
	}
}

// At startup Assurance compares the network with the SSoT and provisions the SSoT again where they
// differ: missing and changed policies are set, IEL policies the SSoT does not have are withdrawn.
func TestStartupRepairsDrift(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for _, a := range [][2]string{{"IN1-OUT1", "preset-1"}, {"IN2-OUT2", "preset-3"}} {
		if _, err := f.uc.ApplyPresetToCell.Run(ctx, a[0], a[1], nil); err != nil {
			t.Fatal(err)
		}
		if _, err := f.uc.TakeCell.Run(ctx, a[0]); err != nil {
			t.Fatal(err)
		}
	}
	r, err := ssot.NewStore(f.db).Realization(ctx)
	if err != nil || len(r.Policies) != 2 {
		t.Fatalf("realization = %+v, %v", r.Policies, err)
	}
	// While the IEL was down: IN 1's policy vanished, IN 2's changed, and a stale one for OUT 3 stayed.
	moved := r.Policies[1]
	moved.SegmentList = []string{"fd77:dd::", "fd77:ff::2"}
	stale := domain.SRPolicy{Name: "visfox-in-3-out-3-decap-fd77-ff", Headend: "headend-100g-8kdg-encap-c",
		Endpoint: "decap-fd77-ff", Color: 103, SegmentList: []string{"fd77:ac::", "fd77:ff::3"}}
	f.mon = &network{policies: []domain.SRPolicy{moved, stale}}
	f.orch.take()

	uc := f.open(t)
	got := ops(f.orch.take())
	want := []string{
		"sr_policy.set headend-100g-8kdg-encap-a", "switcher.select sw-fd77-a",
		"sr_policy.set headend-100g-8kdg-encap-b", "switcher.select sw-fd77-d",
		"sr_policy.delete headend-100g-8kdg-encap-c",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("repair = %v", got)
	}
	c, _ := uc.GetMatrixCell.Run("IN1-OUT1")
	if *c.HealthState != apitypes.HealthStateHealthy {
		t.Fatalf("health = %s", *c.HealthState)
	}

	// A network that matches the SSoT is left alone.
	r, _ = ssot.NewStore(f.db).Realization(ctx)
	f.mon = &network{policies: r.Policies}
	f.open(t)
	if got := f.orch.take(); len(got) != 0 {
		t.Fatalf("no drift, but sent %v", ops(got))
	}
}

// Hot standby on the testbed wiring: IN 1 and 2 wait on SW1, IN 3 and 4 on SW2 -> SW1 e. Taking IN 4
// keeps every stream on its port: IN 4's feed policy only gains the VVFs, the other feeds are not
// touched, and the switchers select upstream first.
func TestHotStandbyTakeKeepsTheSwitcherPorts(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for _, id := range []string{"IN1-OUT1", "IN2-OUT1", "IN3-OUT1", "IN4-OUT1"} {
		if _, err := f.uc.ApplyPresetToCell.Run(ctx, id, "preset-2", nil); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if _, err := f.uc.TakeCell.Run(ctx, "IN1-OUT1"); err != nil {
		t.Fatal(err)
	}
	f.orch.take()
	segs := func() map[string]string {
		r, err := ssot.NewStore(f.db).Realization(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, p := range r.Policies {
			out[p.Headend] = strings.Join(p.SegmentList, ",")
		}
		return out
	}
	before := segs()
	if before["headend-100g-8kdg-encap-d"] != "fd77:dd::,fd77:ae::,fd77:ff::1" {
		t.Fatalf("IN 4 feed = %s", before["headend-100g-8kdg-encap-d"])
	}

	if _, err := f.uc.TakeCell.Run(ctx, "IN4-OUT1"); err != nil {
		t.Fatal(err)
	}
	got := f.orch.take()
	var sets, selects []string
	for _, in := range got {
		switch in.Op {
		case domain.OpSRPolicySet:
			sets = append(sets, in.Target)
		case domain.OpSwitcherSelect:
			selects = append(selects, in.Target+" "+in.Select.Port)
		}
	}
	// IN 4's policy, then IN 1 stepping down to its feed.
	if !slices.Equal(sets, []string{"headend-100g-8kdg-encap-d", "headend-100g-8kdg-encap-a"}) {
		t.Fatalf("policies set = %v", sets)
	}
	if !slices.Equal(selects, []string{"sw-fd77-d d", "sw-fd77-a e"}) {
		t.Fatalf("selects = %v", selects)
	}
	after := segs()
	if d := after["headend-100g-8kdg-encap-d"]; !strings.HasPrefix(d, "fd77:dd::,fd77:ae::,") {
		t.Fatalf("IN 4 on air = %s", d)
	}
	for _, h := range []string{"headend-100g-8kdg-encap-b", "headend-100g-8kdg-encap-c"} {
		if after[h] != before[h] {
			t.Errorf("%s moved: %s -> %s", h, before[h], after[h])
		}
	}
	if after["headend-100g-8kdg-encap-a"] != "fd77:aa::,fd77:ff::1" {
		t.Errorf("IN 1 feed = %s", after["headend-100g-8kdg-encap-a"])
	}
}

// Hot standby: a STANDBY IN is already sent into a switcher feeding the OUT (feed policy up to the
// decap without VVFs), the Take turns its policy into the whole path and switches, and the stream
// stepping down turns back into a feed.
func TestHotStandbyTakeOnlySwitches(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for _, a := range [][2]string{{"IN1-OUT1", "preset-1"}, {"IN2-OUT1", "preset-2"}} {
		if _, err := f.uc.ApplyPresetToCell.Run(ctx, a[0], a[1], nil); err != nil {
			t.Fatal(err)
		}
	}
	got := f.orch.take()
	if !slices.Equal(ops(got), []string{"sr_policy.set headend-100g-8kdg-encap-a", "sr_policy.set headend-100g-8kdg-encap-b"}) ||
		strings.Join(got[0].Policy.SegmentList, ",") != "fd77:aa::,fd77:ff::1" ||
		strings.Join(got[1].Policy.SegmentList, ",") != "fd77:ab::,fd77:ff::1" {
		t.Fatalf("standby feeds = %v", got)
	}

	if _, err := f.uc.TakeCell.Run(ctx, "IN1-OUT1"); err != nil {
		t.Fatal(err)
	}
	if got := ops(f.orch.take()); !slices.Equal(got, []string{
		"function.set_params caption-fd77-ba", "function.set_params color-transcode-crop-fd77-ca",
		"sr_policy.set headend-100g-8kdg-encap-a", "switcher.select sw-fd77-a",
	}) {
		t.Fatalf("take = %v", got)
	}
	if _, err := f.uc.TakeCell.Run(ctx, "IN2-OUT1"); err != nil {
		t.Fatal(err)
	}
	got = f.orch.take()
	if !slices.Equal(ops(got), []string{
		"function.set_params caption-fd77-bb", "function.set_params color-transcode-crop-fd77-cb",
		"sr_policy.set headend-100g-8kdg-encap-b", "switcher.select sw-fd77-a",
		// IN 1 stepped down: back to its feed, nothing is withdrawn
		"sr_policy.set headend-100g-8kdg-encap-a",
	}) || strings.Join(got[4].Policy.SegmentList, ",") != "fd77:aa::,fd77:ff::1" {
		t.Fatalf("second take = %v", ops(got))
	}

	// The topology shows IN 2 on air and IN 1 sent up to the switcher (standby), nothing else.
	var standby []string
	for _, l := range f.uc.GetTopology.Run().Links {
		if l.Standby != nil && *l.Standby {
			standby = append(standby, l.Source+">"+l.Target+" "+strings.Join(*l.CellIds, ","))
		}
	}
	if !slices.Equal(standby, []string{"in-1>sw-fd77-a IN1-OUT1"}) {
		t.Fatalf("standby links = %v", standby)
	}

	// The switcher and IN pools are reserved at STANDBY.
	for _, p := range f.uc.GetResourcePools.Run() {
		if p.Id == "source_unicast:in-1" && (p.ReservedAt != apitypes.ResourcePoolReservedAtStandby || len(p.UsedBy) != 1 || p.UsedBy[0].OnAir) {
			t.Fatalf("pool = %+v", p)
		}
	}
	// Clearing a STANDBY cell withdraws its feed.
	if _, err := f.uc.ClearCellPreset.Run(ctx, "IN1-OUT1"); err != nil {
		t.Fatal(err)
	}
	if got := ops(f.orch.take()); !slices.Equal(got, []string{"sr_policy.delete headend-100g-8kdg-encap-a"}) {
		t.Fatalf("clear = %v", got)
	}
}

// Hot standby on the testbed: an IN sent to one OUT cannot be STANDBY on another; the IEL names the
// STANDBY cells to release, and the assignment with release_cells turns them OFF in the same change.
// ON AIR cells are never offered.
func TestHotStandbyRelease(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if _, err := f.uc.ApplyPresetToCell.Run(ctx, "IN1-OUT1", "preset-1", nil); err != nil {
		t.Fatal(err)
	}
	v, err := f.uc.ValidateMatrixAssignment.Run("IN1-OUT2", "preset-1", nil)
	if err != nil || v.Feasible {
		t.Fatalf("validate = %+v, %v", v, err)
	}
	issue := (*v.Errors)[0]
	if *issue.ResourceKind != ssot.PoolSourceUnicast || issue.ReleasableCells == nil ||
		!slices.Equal(*issue.ReleasableCells, []string{"IN1-OUT1"}) {
		t.Fatalf("issue = %+v", issue)
	}
	if _, err := f.uc.ApplyPresetToCell.Run(ctx, "IN1-OUT2", "preset-1", nil); code(t, err) != apitypes.ErrorCodeINFEASIBLEASSIGNMENT {
		t.Fatalf("apply without release = %v", err)
	}
	f.orch.take()
	c, err := f.uc.ApplyPresetToCell.Run(ctx, "IN1-OUT2", "preset-1", []string{"IN1-OUT1"})
	if err != nil || c.ExecutionState != apitypes.ExecutionStateStandby || state(t, f.uc, "IN1-OUT1") != apitypes.ExecutionStateOff {
		t.Fatalf("apply with release = %+v, %v", c, err)
	}
	if got := ops(f.orch.take()); !slices.Equal(got, []string{
		"sr_policy.delete headend-100g-8kdg-encap-a", // feed to OUT 1 first (one unicast)
		"sr_policy.set headend-100g-8kdg-encap-a",    // then the feed to OUT 2
	}) {
		t.Fatalf("release = %v", got)
	}

	// On air, the IN is not offered for release.
	if _, err := f.uc.TakeCell.Run(ctx, "IN1-OUT2"); err != nil {
		t.Fatal(err)
	}
	v, _ = f.uc.ValidateMatrixAssignment.Run("IN1-OUT1", "preset-1", nil)
	if v.Feasible || (*v.Errors)[0].ReleasableCells != nil {
		t.Fatalf("validate = %+v", (*v.Errors)[0])
	}
	if _, err := f.uc.ApplyPresetToCell.Run(ctx, "IN1-OUT1", "preset-1", []string{"IN1-OUT2"}); code(t, err) != apitypes.ErrorCodeINVALIDCELLSTATE {
		t.Fatalf("releasing an on-air cell = %v", err)
	}
}

// Hot standby on the testbed: one OUT takes all four INs through both switchers (two stages);
// with two OUTs each gets one switcher (three INs at most), and the cascade is given up when an
// IN leaves so that the other OUT gets a switcher.
func TestHotStandbySwitcherTrees(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for _, id := range []string{"IN1-OUT1", "IN2-OUT1", "IN3-OUT1", "IN4-OUT1"} {
		if _, err := f.uc.ApplyPresetToCell.Run(ctx, id, "preset-2", nil); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	// Every IN is sent to OUT 1 and both switchers' outputs are used.
	v, _ := f.uc.ValidateMatrixAssignment.Run("IN4-OUT2", "preset-2", nil)
	if v.Feasible {
		t.Fatal("OUT 2 cannot get IN 4 while OUT 1 uses it")
	}
	if _, err := f.uc.ClearCellPreset.Run(ctx, "IN4-OUT1"); err != nil {
		t.Fatal(err)
	}
	f.orch.take()
	if _, err := f.uc.ApplyPresetToCell.Run(ctx, "IN4-OUT2", "preset-2", nil); err != nil {
		t.Fatalf("OUT 2 after OUT 1 gave up its cascade: %v", err)
	}
	for _, id := range []string{"IN1-OUT1", "IN4-OUT2"} {
		p, err := f.uc.GetCellPath.Run(id)
		if err != nil {
			t.Fatal(err)
		}
		if n := slices.IndexFunc(p.Nodes, func(n apitypes.PathNode) bool { return n.Switcher != nil }); n < 0 ||
			p.Nodes[n+1].Switcher != nil {
			t.Fatalf("%s should use one switcher: %+v", id, p.Nodes)
		}
	}
	// A third OUT has no switcher left (and no IN either).
	v, _ = f.uc.ValidateMatrixAssignment.Run("IN1-OUT3", "preset-2", nil)
	if v.Feasible {
		t.Fatal("OUT 3 must not fit")
	}
}

// The rich network (one switcher per OUT, eight converters, no IN unicast limit): every OUT can hold
// every IN in STANDBY and put the same IN on air at once; no release is ever needed.
func TestRichNetwork(t *testing.T) {
	ctx := context.Background()
	f := (&fixture{network: "rich"}).init(t, false, "../../../db/presets.yaml")
	for _, out := range []string{"OUT1", "OUT2", "OUT3", "OUT4"} {
		for _, in := range []string{"IN1", "IN2", "IN3", "IN4"} {
			if _, err := f.uc.ApplyPresetToCell.Run(ctx, in+"-"+out, "preset-2", nil); err != nil {
				t.Fatalf("%s-%s: %v", in, out, err)
			}
		}
		if _, err := f.uc.TakeCell.Run(ctx, "IN1-"+out); err != nil {
			t.Fatalf("take IN1-%s: %v", out, err)
		}
	}
	if got := len(f.uc.GetSwitchers.Run()); got != 4 {
		t.Fatalf("switchers = %d", got)
	}
	// IN 1 goes to four OUTs, each through its own switcher.
	switchers := map[string]bool{}
	for _, out := range []string{"OUT1", "OUT2", "OUT3", "OUT4"} {
		p, err := f.uc.GetCellPath.Run("IN1-" + out)
		if err != nil {
			t.Fatal(err)
		}
		switchers[p.Nodes[1].NodeId] = true
	}
	if len(switchers) != 4 {
		t.Fatalf("switchers on air = %v", switchers)
	}
}

// Intents that no longer fit after the operator config got stricter (cold -> hot standby) stay in the
// SSoT: their STANDBY cells say why they cannot be taken, and they do not block changes elsewhere.
// The IN stays with the OUT that has it on air, even when a STANDBY of another OUT comes first.
func TestStricterConfigKeepsOtherChanges(t *testing.T) {
	ctx := context.Background()
	f := newColdFixture(t)
	for _, id := range []string{"IN1-OUT1", "IN1-OUT2"} {
		if _, err := f.uc.ApplyPresetToCell.Run(ctx, id, "preset-2", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.uc.TakeCell.Run(ctx, "IN1-OUT2"); err != nil {
		t.Fatal(err)
	}
	f.limits = nil // the shipped config: hot standby
	uc := f.open(t)
	// Taking it would take IN 1 from OUT 2's picture: that is the first reason.
	c, _ := uc.GetMatrixCell.Run("IN1-OUT1")
	if c.ExecutionState != apitypes.ExecutionStateStandby || len(*c.TakeIssues) == 0 ||
		*(*c.TakeIssues)[0].ResourceKind != ssot.PoolSourceUnicast {
		t.Fatalf("over-subscribed cell = %+v", c.TakeIssues)
	}
	p, err := uc.GetCellPath.Run("IN1-OUT2")
	if err != nil || p.Nodes[1].Switcher == nil {
		t.Fatalf("the on-air IN lost its switcher: %+v, %v", p.Nodes, err)
	}
	if _, err := uc.ApplyPresetToCell.Run(ctx, "IN2-OUT2", "preset-2", nil); err != nil {
		t.Fatalf("a change on OUT 2 is blocked by OUT 1: %v", err)
	}
	if _, err := uc.TakeCell.Run(ctx, "IN1-OUT1"); code(t, err) != apitypes.ErrorCodeRESOURCEEXHAUSTED {
		t.Fatalf("take of the over-subscribed cell = %v", err)
	}
}

// Hot standby: when the OUT has no switcher left and its IN is sent elsewhere too, the switcher is
// the first reason; no release is offered when ON AIR cells hold the switchers.
func TestHotStandbySwitcherShortageFirst(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for _, id := range []string{"IN1-OUT1", "IN2-OUT1", "IN4-OUT2"} {
		if _, err := f.uc.ApplyPresetToCell.Run(ctx, id, "preset-2", nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"IN1-OUT1", "IN4-OUT2"} {
		if _, err := f.uc.TakeCell.Run(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	v, err := f.uc.ValidateMatrixAssignment.Run("IN2-OUT3", "preset-2", nil)
	if err != nil || v.Feasible {
		t.Fatalf("validate = %+v, %v", v, err)
	}
	var kinds []string
	for _, e := range *v.Errors {
		kinds = append(kinds, *e.ResourceKind)
		if e.ReleasableCells != nil {
			t.Fatalf("release offered: %v", *e.ReleasableCells)
		}
	}
	// IN 2 could enter SW1 once for both OUTs (a shared input): the switcher outputs are short.
	if len(kinds) == 0 || kinds[0] != ssot.PoolVVFOutput {
		t.Fatalf("issues = %v", kinds)
	}
	// With a free switcher only the IN is short, and releasing its STANDBY solves it.
	v, _ = f.uc.ValidateMatrixAssignment.Run("IN2-OUT2", "preset-2", nil)
	if e := (*v.Errors)[0]; len(*v.Errors) != 1 || *e.ResourceKind != ssot.PoolSourceUnicast ||
		e.ReleasableCells == nil || !slices.Equal(*e.ReleasableCells, []string{"IN2-OUT1"}) {
		t.Fatalf("issues = %+v", *v.Errors)
	}
}

// The release is only for moving an IN already sent to another OUT: a shortage that turning off
// another IN's STANDBY would solve (here the switchers) is reported, not offered as a release.
func TestReleaseOnlyTheSameIN(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for _, id := range []string{"IN1-OUT1", "IN2-OUT2"} {
		if _, err := f.uc.ApplyPresetToCell.Run(ctx, id, "preset-2", nil); err != nil {
			t.Fatal(err)
		}
	}
	v, _ := f.uc.ValidateMatrixAssignment.Run("IN3-OUT3", "preset-2", nil)
	if v.Feasible || (*v.Errors)[0].ReleasableCells != nil || *(*v.Errors)[0].ResourceKind != ssot.PoolVVFOutput {
		t.Fatalf("validate = %+v", *v.Errors)
	}
	// The same IN: offered.
	v, _ = f.uc.ValidateMatrixAssignment.Run("IN1-OUT3", "preset-2", nil)
	if v.Feasible || (*v.Errors)[0].ReleasableCells == nil || !slices.Equal(*(*v.Errors)[0].ReleasableCells, []string{"IN1-OUT1"}) {
		t.Fatalf("validate = %+v", *v.Errors)
	}
}
