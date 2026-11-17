package usecase_test

// The switching test model: every operation on the matrix (assign, clear, Take, standby) runs
// against a model of the network and is checked for the two requirements of video switching
// (the switching test model):
//
//   - The picture never breaks. After every southbound instruction the model computes what reaches
//     each OUT (the IN whose SR Policy passes the input every switcher selects, with the VVF configs
//     on its way). An OUT the operation is not about keeps its picture throughout; the OUT of a Take
//     (or of a stop / a change of its on-air preset) changes once, from the old picture straight to
//     the new one, never through a third state (black, another IN, a half-configured chain). With
//     hot standby the IN being taken must already arrive on the switcher ports it goes on air
//     through (no re-wiring at the Take).
//   - The resources are used to the full. An independent oracle searches every allocation of the
//     ways through the switchers that keeps the on-air ways (and, at a Take, the taken IN's fed way:
//     the strict rule) and fits the limits. With cold standby a Take must also fit while the IN
//     stepping down is still on air (its way kept, the new IN beside it in the same tree). An
//     operation is refused if and only if no such allocation exists.
//
// Besides: the predictions (validate, take_issues) match the outcome, a refused operation sends
// nothing, the network holds exactly the SR Policies of the SSoT, every takeable STANDBY IN is fed to
// its OUT's switchers, and the allocation the IEL reports fits the limits.
//
// The model handles select nodes in filter mode with one output (the shipped networks).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/netip"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/config"
	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot/ted"
)

type hop struct{ node, port string }

func (h hop) String() string { return h.node + ":" + h.port }

// switchNet is what the model knows about the network (from the same TED and config as the IEL).
type switchNet struct {
	res     *ssot.Resources
	flow    map[string]ssot.FlowNode
	sidHop  map[string]hop    // switcher input SID -> switcher input
	sidNode map[string]string // VVF (processing) SID -> node
	decap   map[string]bool
	color   map[string]uint32 // OUT -> SR Policy color
	inOf    map[string]string // headend node -> IN
	hot     bool
}

func loadNet(t *testing.T, f *fixture) *switchNet {
	t.Helper()
	suffix := ""
	if f.network != "" {
		suffix = "-" + f.network
	}
	op, err := config.LoadOperator("../../../config/operator" + suffix + ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := ted.SeedFile{Path: "../../../db/ted" + suffix + ".yaml"}.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tg, err := ted.Interpret(dev, op.SIDRule, op.Bindings())
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
	n := &switchNet{res: res, flow: map[string]ssot.FlowNode{}, sidHop: map[string]hop{}, sidNode: map[string]string{},
		decap: map[string]bool{}, color: map[string]uint32{}, inOf: map[string]string{}, hot: res.Limits.HotStandby()}
	for _, fn := range res.FlowNodes {
		if fn.Behavior != domain.BehaviorSelect || fn.Mode == "swap" || fn.Ports.Outputs != 1 {
			t.Fatalf("the switching model handles filter select nodes with one output, not %+v", fn)
		}
		n.flow[fn.ID] = fn
	}
	for _, node := range tg.Nodes {
		switch {
		case node.Role == domain.RoleDecap:
			for _, s := range node.SIDs {
				n.decap[s] = true
			}
		case len(node.Inputs) > 0:
			for _, in := range node.Inputs {
				n.sidHop[in.SID] = hop{node.ID, in.Port}
			}
		default:
			for _, s := range node.SIDs {
				n.sidNode[s] = node.ID
			}
		}
	}
	for i, d := range tg.Destinations {
		n.color[d.ID] = 100 + uint32(i+1)
	}
	for _, s := range tg.Sources {
		n.inOf[s.NodeID] = s.ID
	}
	return n
}

// norm drops the argument (the low 64 bits) of a SID the TED does not know as is.
func (n *switchNet) norm(sid string) string {
	if _, ok := n.sidHop[sid]; ok {
		return sid
	}
	if _, ok := n.sidNode[sid]; ok || n.decap[sid] {
		return sid
	}
	a, err := netip.ParseAddr(sid)
	if err != nil {
		return sid
	}
	b := a.As16()
	for i := 8; i < 16; i++ {
		b[i] = 0
	}
	return netip.AddrFrom16(b).String()
}

// hops are the switcher inputs a Segment List goes through, in order.
func (n *switchNet) hops(segs []string) []hop {
	var out []hop
	for _, s := range segs {
		if h, ok := n.sidHop[n.norm(s)]; ok {
			out = append(out, h)
		}
	}
	return out
}

// netState is the network as the southbound instructions leave it.
type netState struct {
	policies map[string]domain.SRPolicy
	selects  map[string]string
	configs  map[string]string
}

func newNetState() *netState {
	return &netState{policies: map[string]domain.SRPolicy{}, selects: map[string]string{}, configs: map[string]string{}}
}

func policyKey(p domain.SRPolicy) string {
	return fmt.Sprintf("%s|%d|%s", p.Headend, p.Color, p.Endpoint)
}

func (s *netState) apply(in domain.Instruction) {
	switch in.Op {
	case domain.OpSRPolicySet:
		s.policies[policyKey(*in.Policy)] = *in.Policy
	case domain.OpSRPolicyDelete:
		delete(s.policies, policyKey(*in.Policy))
	case domain.OpSwitcherSelect:
		s.selects[in.Target] = in.Select.Port
	case domain.OpFunctionSetParams:
		var parts []string
		for _, fn := range in.Functions {
			b, _ := json.Marshal(fn.Params)
			parts = append(parts, fn.Type+string(b))
		}
		s.configs[in.Target] = strings.Join(parts, ";")
	}
}

// picture is what reaches the OUT: every stream whose policy to the OUT passes the input each of its
// switchers selects and ends at the decap ("" when none; more than one is a broken picture).
func (n *switchNet) picture(s *netState, out string) string {
	var streams []string
	for _, p := range s.policies {
		if p.Color != n.color[out] || len(p.SegmentList) == 0 || !n.decap[n.norm(p.SegmentList[len(p.SegmentList)-1])] {
			continue
		}
		passes := true
		var vvfs []string
		for _, seg := range p.SegmentList {
			if h, ok := n.sidHop[n.norm(seg)]; ok && s.selects[h.node] != h.port {
				passes = false
			}
			if node, ok := n.sidNode[n.norm(seg)]; ok {
				vvfs = append(vvfs, node+"="+s.configs[node])
			}
		}
		if passes {
			streams = append(streams, n.inOf[p.Headend]+" ["+strings.Join(p.SegmentList, ",")+"] "+strings.Join(vvfs, " "))
		}
	}
	sort.Strings(streams)
	return strings.Join(streams, " + ")
}

// ---------------------------------------------------------------------------------------- oracle

// flowWant is what one OUT needs: a way for every IN it is fed, some of them fixed.
type flowWant struct {
	out   string
	fed   []string
	fixed map[string][]hop
}

// ways are every way from the IN through the switchers to a switcher that feeds the OUT.
func (n *switchNet) ways(in, out string) [][]hop {
	var res [][]hop
	var walk func(node string, after []hop, seen map[string]bool)
	walk = func(node string, after []hop, seen map[string]bool) {
		if seen[node] {
			return
		}
		seen[node] = true
		defer delete(seen, node)
		for _, i := range n.flow[node].Inputs {
			way := append([]hop{{node, i.Port}}, after...)
			if i.From == in {
				res = append(res, way)
			}
			if _, ok := n.flow[i.From]; ok {
				walk(i.From, way, seen)
			}
		}
	}
	for id, fn := range n.flow {
		if slices.Contains(fn.Destinations, out) {
			walk(id, nil, map[string]bool{})
		}
	}
	return res
}

// check returns why an allocation (OUT -> IN -> way) breaks the limits, "" when it fits.
func (n *switchNet) check(alloc map[string]map[string][]hop) string {
	outsOf := map[string]map[string]bool{}      // switcher -> OUTs whose tree has it
	portUse := map[string]map[string][]string{} // switcher -> port -> OUTs
	unicast := map[string]map[string]bool{}     // IN -> first hops
	for _, out := range slices.Sorted(maps.Keys(alloc)) {
		down := map[string]string{}
		root := ""
		for _, in := range slices.Sorted(maps.Keys(alloc[out])) {
			way := alloc[out][in]
			if len(way) == 0 {
				return fmt.Sprintf("%s: %s has no way", out, in)
			}
			r := way[len(way)-1].node
			if root != "" && r != root {
				return fmt.Sprintf("%s: two roots %s and %s", out, root, r)
			}
			root = r
			if !slices.Contains(n.flow[r].Destinations, out) {
				return fmt.Sprintf("%s: %s does not feed it", out, r)
			}
			for i, h := range way {
				next := ""
				if i+1 < len(way) {
					next = way[i+1].String()
				}
				if d, ok := down[h.node]; ok && d != next {
					return fmt.Sprintf("%s: %s sends to two places", out, h.node)
				}
				down[h.node] = next
				if outsOf[h.node] == nil {
					outsOf[h.node] = map[string]bool{}
				}
				outsOf[h.node][out] = true
				if portUse[h.node] == nil {
					portUse[h.node] = map[string][]string{}
				}
				if !slices.Contains(portUse[h.node][h.port], out) {
					portUse[h.node][h.port] = append(portUse[h.node][h.port], out)
				}
			}
			if unicast[in] == nil {
				unicast[in] = map[string]bool{}
			}
			unicast[in][way[0].String()] = true
		}
	}
	for node, outs := range outsOf {
		if c := n.flow[node].Ports.Outputs; c > 0 && len(outs) > c {
			return fmt.Sprintf("%s: %d outputs > %d", node, len(outs), c)
		}
	}
	for node, ports := range portUse {
		if c := n.flow[node].Ports.Inputs; c > 0 && len(ports) > c {
			return fmt.Sprintf("%s: %d inputs > %d", node, len(ports), c)
		}
		for port, outs := range ports {
			i := slices.IndexFunc(n.flow[node].Inputs, func(x apitypes.SwitcherInput) bool { return x.Port == port })
			if _, fromSwitcher := n.flow[n.flow[node].Inputs[i].From]; fromSwitcher && len(outs) > 1 {
				return fmt.Sprintf("%s:%s carries a selected stream for %v", node, port, outs)
			}
		}
	}
	if c := n.res.Limits.SourceUnicasts; c > 0 {
		for in, hops := range unicast {
			if len(hops) > c {
				return fmt.Sprintf("%s: %d unicasts > %d", in, len(hops), c)
			}
		}
	}
	return ""
}

// feasible reports whether every OUT gets a way for every IN it is fed within the limits, keeping
// the fixed ways (exhaustive search).
func (n *switchNet) feasible(wants []flowWant) bool {
	type slot struct {
		out, in string
		ways    [][]hop
	}
	var slots []slot
	for _, w := range wants {
		for _, in := range w.fed {
			if fixed, ok := w.fixed[in]; ok {
				slots = append(slots, slot{w.out, in, [][]hop{fixed}})
				continue
			}
			slots = append(slots, slot{w.out, in, n.ways(in, w.out)})
		}
	}
	alloc := map[string]map[string][]hop{}
	var search func(i int) bool
	search = func(i int) bool {
		if i == len(slots) {
			return true
		}
		s := slots[i]
		if alloc[s.out] == nil {
			alloc[s.out] = map[string][]hop{}
		}
		for _, way := range s.ways {
			alloc[s.out][s.in] = way
			if n.check(alloc) == "" && search(i+1) {
				return true
			}
		}
		delete(alloc[s.out], s.in)
		return false
	}
	return search(0)
}

// ------------------------------------------------------------------------------------- operations

type opKind string

const (
	opAssign  opKind = "assign"
	opClear   opKind = "clear"
	opTake    opKind = "take"
	opStandby opKind = "standby"
)

type switchOp struct {
	kind    opKind
	cell    string
	preset  string
	release []string
}

func (o switchOp) String() string {
	s := string(o.kind) + " " + o.cell
	if o.preset != "" {
		s += " " + o.preset
	}
	if len(o.release) > 0 {
		s += fmt.Sprintf(" release %v", o.release)
	}
	return s
}

// switching runs operations on a fixture and checks the model after each.
type switching struct {
	t      *testing.T
	f      *fixture
	net    *switchNet
	state  *netState
	oracle bool
	log    []string
}

func newSwitching(t *testing.T, f *fixture, oracle bool) *switching {
	t.Helper()
	f.orch.take()
	return &switching{t: t, f: f, net: loadNet(t, f), state: newNetState(), oracle: oracle}
}

func (s *switching) fail(format string, args ...any) {
	s.t.Helper()
	s.t.Fatalf("%s\nsteps:\n  %s", fmt.Sprintf(format, args...), strings.Join(s.log, "\n  "))
}

func (s *switching) cells() map[string]apitypes.MatrixCell {
	out := map[string]apitypes.MatrixCell{}
	for _, c := range s.f.uc.GetMatrixState.Run().Cells {
		out[c.CellId] = c
	}
	return out
}

// path is the switcher inputs of a cell's way ("" ways for cells without one).
func (s *switching) path(cell string) []hop {
	p, err := s.f.uc.GetCellPath.Run(cell)
	if err != nil {
		return nil
	}
	var out []hop
	for _, n := range p.Nodes {
		if n.Switcher != nil {
			out = append(out, hop{n.NodeId, n.Switcher.InputPort})
		}
	}
	return out
}

func applied(c apitypes.MatrixCell) bool {
	return c.ExecutionState == apitypes.ExecutionStateActive || c.ExecutionState == apitypes.ExecutionStateStandby
}

// fed returns the cells fed to their OUT's switchers by OUT (on-air first).
func (s *switching) fed(cells map[string]apitypes.MatrixCell) map[string][]apitypes.MatrixCell {
	out := map[string][]apitypes.MatrixCell{}
	for _, id := range slices.Sorted(maps.Keys(cells)) {
		c := cells[id]
		if c.ExecutionState == apitypes.ExecutionStateActive || (s.net.hot && c.ExecutionState == apitypes.ExecutionStateStandby) {
			out[c.DestinationId] = append(out[c.DestinationId], c)
		}
	}
	for d := range out {
		slices.SortStableFunc(out[d], func(a, b apitypes.MatrixCell) int {
			return boolInt(b.ExecutionState == apitypes.ExecutionStateActive) - boolInt(a.ExecutionState == apitypes.ExecutionStateActive)
		})
	}
	return out
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func onAirOf(cells map[string]apitypes.MatrixCell, out string) string {
	for _, c := range cells {
		if c.DestinationId == out && c.ExecutionState == apitypes.ExecutionStateActive {
			return c.CellId
		}
	}
	return ""
}

// after is the cells' states the operation asks for (whether or not the IEL accepts it).
func after(cells map[string]apitypes.MatrixCell, o switchOp) map[string]apitypes.ExecutionState {
	st := map[string]apitypes.ExecutionState{}
	for id, c := range cells {
		st[id] = c.ExecutionState
	}
	c := cells[o.cell]
	switch o.kind {
	case opAssign:
		if c.ExecutionState != apitypes.ExecutionStateActive {
			st[o.cell] = apitypes.ExecutionStateStandby
		}
		for _, r := range o.release {
			st[r] = apitypes.ExecutionStateOff
		}
	case opClear:
		st[o.cell] = apitypes.ExecutionStateOff
	case opTake:
		for id, x := range cells {
			if x.DestinationId == c.DestinationId && x.ExecutionState == apitypes.ExecutionStateActive {
				st[id] = apitypes.ExecutionStateStandby
			}
		}
		st[o.cell] = apitypes.ExecutionStateActive
	case opStandby:
		st[o.cell] = apitypes.ExecutionStateStandby
	}
	return st
}

// wants builds the oracle's question for the operation: the INs every OUT is fed after it, with the
// on-air ways that stay fixed (strict rule).
func (s *switching) wants(cells map[string]apitypes.MatrixCell, paths map[string][]hop, o switchOp) []flowWant {
	st := after(cells, o)
	byOut := map[string]*flowWant{}
	for _, id := range slices.Sorted(maps.Keys(st)) {
		c := cells[id]
		fed := st[id] == apitypes.ExecutionStateActive || (s.net.hot && st[id] == apitypes.ExecutionStateStandby)
		// A cold standby Take: the IN stepping down stays on air on its way until the switch.
		stepping := !s.net.hot && o.kind == opTake && id != o.cell && c.DestinationId == cells[o.cell].DestinationId &&
			c.ExecutionState == apitypes.ExecutionStateActive
		if !fed && !stepping {
			continue
		}
		w := byOut[c.DestinationId]
		if w == nil {
			w = &flowWant{out: c.DestinationId, fixed: map[string][]hop{}}
			byOut[c.DestinationId] = w
		}
		w.fed = append(w.fed, c.SourceId)
		if stepping && len(paths[id]) > 0 {
			w.fixed[c.SourceId] = paths[id]
			continue
		}
		if st[id] != apitypes.ExecutionStateActive || len(paths[id]) == 0 {
			continue
		}
		stays := c.ExecutionState == apitypes.ExecutionStateActive
		taken := o.kind == opTake && id == o.cell && s.net.hot
		if stays || taken {
			w.fixed[c.SourceId] = paths[id]
		}
	}
	var out []flowWant
	for _, d := range slices.Sorted(maps.Keys(byOut)) {
		out = append(out, *byOut[d])
	}
	return out
}

func resourceRefusal(err error) (bool, apitypes.ErrorCode) {
	var e *domain.Error
	if !errors.As(err, &e) {
		return false, ""
	}
	return e.Code == apitypes.ErrorCodeINFEASIBLEASSIGNMENT || e.Code == apitypes.ErrorCodeRESOURCEEXHAUSTED, e.Code
}

// do runs one operation and checks the model. It returns whether the IEL accepted it (false also
// for operations the cell's state does not allow).
func (s *switching) do(o switchOp) bool {
	s.t.Helper()
	ctx := context.Background()
	cells := s.cells()
	c, ok := cells[o.cell]
	if !ok {
		s.fail("no cell %s", o.cell)
	}
	paths := map[string][]hop{}
	for id, x := range cells {
		if applied(x) {
			paths[id] = s.path(id)
		}
	}
	before := map[string]string{}
	for out := range s.net.color {
		before[out] = s.net.picture(s.state, out)
	}

	// The prediction the WebUI shows before the operator acts.
	predicted := true
	switch o.kind {
	case opAssign:
		v, err := s.f.uc.ValidateMatrixAssignment.Run(o.cell, o.preset, o.release)
		if err != nil {
			s.fail("validate %s: %v", o, err)
		}
		predicted = v.Feasible
	case opTake:
		predicted = c.TakeIssues == nil || len(*c.TakeIssues) == 0
	}

	var err error
	switch o.kind {
	case opAssign:
		_, err = s.f.uc.ApplyPresetToCell.Run(ctx, o.cell, o.preset, o.release)
	case opClear:
		_, err = s.f.uc.ClearCellPreset.Run(ctx, o.cell)
	case opTake:
		_, err = s.f.uc.TakeCell.Run(ctx, o.cell)
	case opStandby:
		_, err = s.f.uc.StandbyCell.Run(ctx, o.cell)
	}
	ins := s.f.orch.take()
	refused, code := resourceRefusal(err)
	switch {
	case err == nil:
		s.log = append(s.log, o.String()+": ok")
	case code == apitypes.ErrorCodeINVALIDCELLSTATE:
		if len(ins) > 0 {
			s.fail("%s: refused for the cell state but sent %v", o, ops(ins))
		}
		return false
	case refused:
		s.log = append(s.log, o.String()+": refused ("+string(code)+")")
	default:
		s.fail("%s: %v", o, err)
	}
	accepted := err == nil

	// Predictions match the outcome; a refusal sends nothing.
	if (o.kind == opAssign || o.kind == opTake) && predicted != accepted {
		s.fail("%s: predicted feasible=%v but accepted=%v (%v)", o, predicted, accepted, err)
	}
	if !accepted && len(ins) > 0 {
		s.fail("%s: refused but sent %v", o, ops(ins))
	}

	// The picture of every OUT, after every instruction.
	target := c.DestinationId
	changes := accepted && (o.kind == opTake || o.kind == opStandby ||
		(c.ExecutionState == apitypes.ExecutionStateActive && (o.kind == opAssign || o.kind == opClear)))
	trace := map[string][]string{}
	for out := range s.net.color {
		trace[out] = []string{before[out]}
	}
	for _, in := range ins {
		s.state.apply(in)
		for out := range s.net.color {
			trace[out] = append(trace[out], s.net.picture(s.state, out))
		}
	}
	final := map[string]string{}
	nowCells := s.cells()
	for out, tr := range trace {
		final[out] = tr[len(tr)-1]
		if onAirOf(cells, out) == "" && onAirOf(nowCells, out) == "" {
			if slices.ContainsFunc(tr, func(p string) bool { return p != "" }) {
				s.fail("%s: %s has nothing on air but shows %q", o, out, slices.Compact(slices.Clone(tr)))
			}
			continue
		}
		allowed := []string{before[out]}
		if out == target && changes {
			allowed = append(allowed, final[out])
		}
		if o.kind == opStandby && out == target {
			allowed = append(allowed, "")
		}
		switched := false
		for i, p := range tr {
			if !slices.Contains(allowed, p) {
				s.fail("%s: %s shows %q after %v (allowed %q)", o, out, p, ops(ins[:i]), allowed)
			}
			if p != before[out] {
				switched = true
			} else if switched {
				s.fail("%s: %s switched back and forth: %q", o, out, tr)
			}
		}
		if (out != target || !changes) && final[out] != before[out] {
			s.fail("%s: %s changed from %q to %q", o, out, before[out], final[out])
		}
	}

	// The picture each OUT should show.
	now := s.cells()
	for out := range s.net.color {
		id := onAirOf(now, out)
		if id == "" {
			if final[out] != "" {
				s.fail("%s: %s has nothing on air but shows %q", o, out, final[out])
			}
			continue
		}
		if !strings.HasPrefix(final[out], now[id].SourceId+" [") || strings.Contains(final[out], " + ") {
			s.fail("%s: %s should show %s, shows %q", o, out, now[id].SourceId, final[out])
		}
	}

	// Hot standby: the IN goes on air through the switcher inputs it was already fed on.
	if accepted && o.kind == opTake && s.net.hot {
		if !slices.Equal(s.path(o.cell), paths[o.cell]) {
			s.fail("%s: re-wired the taken IN from %v to %v", o, paths[o.cell], s.path(o.cell))
		}
	}

	s.checkState(o)

	// Resources to the full: refused iff no allocation keeping the on-air ways fits.
	if s.oracle {
		ok := s.net.feasible(s.wants(cells, paths, o))
		if accepted && !ok {
			s.fail("%s: accepted, but the oracle finds no allocation that keeps the on-air ways", o)
		}
		if !accepted && ok {
			s.fail("%s: refused, but an allocation keeping the on-air ways fits", o)
		}
	}
	return accepted
}

// checkState checks the invariants of the state an operation left.
func (s *switching) checkState(o switchOp) {
	s.t.Helper()
	ctx := context.Background()
	// The network holds exactly the SR Policies the SSoT records.
	r, err := ssot.NewStore(s.f.db).Realization(ctx)
	if err != nil {
		s.fail("%s: realization: %v", o, err)
	}
	want := map[string]string{}
	for _, p := range r.Policies {
		want[policyKey(p)] = strings.Join(p.SegmentList, ",")
	}
	got := map[string]string{}
	for k, p := range s.state.policies {
		got[k] = strings.Join(p.SegmentList, ",")
	}
	if fmt.Sprint(want) != fmt.Sprint(got) {
		s.fail("%s: network %v, SSoT %v", o, got, want)
	}

	cells := s.cells()
	alloc := map[string]map[string][]hop{}
	for out, list := range s.fed(cells) {
		for _, c := range list {
			p := s.path(c.CellId)
			takeable := c.TakeIssues == nil || len(*c.TakeIssues) == 0
			if c.ExecutionState == apitypes.ExecutionStateStandby && !takeable {
				continue // waits without a feed (shortage reported on the cell)
			}
			if len(p) == 0 {
				s.fail("%s: %s is fed but has no way", o, c.CellId)
			}
			if alloc[out] == nil {
				alloc[out] = map[string][]hop{}
			}
			alloc[out][c.SourceId] = p
			// Hot standby: a takeable STANDBY IN already reaches its switchers.
			if c.ExecutionState == apitypes.ExecutionStateStandby {
				fed := false
				for _, pol := range s.state.policies {
					if pol.Color == s.net.color[out] && s.net.inOf[pol.Headend] == c.SourceId &&
						slices.Equal(s.net.hops(pol.SegmentList), p) {
						fed = true
					}
				}
				if !fed {
					s.fail("%s: STANDBY %s is takeable but not fed on %v", o, c.CellId, p)
				}
			}
		}
	}
	// The allocation fits the limits.
	if why := s.net.check(alloc); why != "" {
		s.fail("%s: allocation breaks a limit: %s (%v)", o, why, alloc)
	}
}

// ------------------------------------------------------------------------------------- scenarios

// flowFixture is the testbed whose VVF nodes never run short (one config for every cell, no stream
// limit), so the switchers and IN unicasts alone decide.
func flowFixture(t *testing.T, network string) *fixture {
	t.Helper()
	f := &fixture{network: network, limits: func(l *ssot.Limits) { l.VVFStreams = nil }}
	return f.init(t, presetsFor(network))
}

// coldFlowFixture is flowFixture on the testbed with cold standby.
func coldFlowFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{limits: func(l *ssot.Limits) { l.VVFStreams = nil; l.StandbyFeed = ssot.StandbyFeedNone }}
	return f.init(t, presetsFor(""))
}

func presetsFor(network string) string {
	if network == "" {
		return "../../../db/presets.yaml"
	}
	return "../../../db/presets-" + network + ".yaml"
}

const plain = "preset-3" // Crop Center: converter VVF only, no pinned node

type step struct {
	op     switchOp
	accept bool
}

func assign(cell string, release ...string) switchOp {
	return switchOp{kind: opAssign, cell: cell, preset: plain, release: release}
}
func take(cell string) switchOp    { return switchOp{kind: opTake, cell: cell} }
func standby(cell string) switchOp { return switchOp{kind: opStandby, cell: cell} }
func clearCell(cell string) switchOp {
	return switchOp{kind: opClear, cell: cell}
}

// fourOnOut1 feeds IN 1-4 to OUT 1 with IN 1 on air: on the testbed IN 1 and 2 wait on SW1, IN 3 and
// 4 on SW2 -> SW1 e.
var fourOnOut1 = []step{
	{assign("IN1-OUT1"), true}, {assign("IN2-OUT1"), true}, {assign("IN3-OUT1"), true}, {assign("IN4-OUT1"), true},
	{take("IN1-OUT1"), true},
}

func TestSwitchingScenarios(t *testing.T) {
	scenarios := []struct {
		name  string
		steps []step
	}{
		{"take every IN in turn, back and forth", slices.Concat(fourOnOut1, []step{
			{take("IN4-OUT1"), true}, {take("IN3-OUT1"), true}, {take("IN2-OUT1"), true},
			{take("IN4-OUT1"), true}, {take("IN1-OUT1"), true},
		})},
		{"split 4+0 into 3+1 from a direct on-air IN (clear, then assign)", slices.Concat(fourOnOut1, []step{
			{clearCell("IN4-OUT1"), true}, {assign("IN4-OUT2"), true}, {take("IN4-OUT2"), true},
			{take("IN3-OUT1"), true}, {take("IN2-OUT1"), true},
		})},
		{"split by releasing the other OUT's STANDBY", slices.Concat(fourOnOut1, []step{
			{assign("IN3-OUT2", "IN3-OUT1"), true}, {take("IN3-OUT2"), true}, {take("IN4-OUT1"), true},
		})},
		{"an IN on air through the cascade keeps both switchers (strict)", slices.Concat(fourOnOut1, []step{
			{take("IN4-OUT1"), true},
			{clearCell("IN3-OUT1"), true}, {assign("IN3-OUT2"), false},
			// Taking a direct IN frees the cascade for OUT 2.
			{take("IN1-OUT1"), true}, {assign("IN3-OUT2"), true}, {take("IN3-OUT2"), true},
		})},
		{"stop and resume output", slices.Concat(fourOnOut1, []step{
			{standby("IN1-OUT1"), true}, {take("IN4-OUT1"), true}, {standby("IN4-OUT1"), true}, {take("IN2-OUT1"), true},
		})},
		{"two OUTs with one switcher each, a third gets none", []step{
			{assign("IN1-OUT1"), true}, {assign("IN2-OUT1"), true}, {take("IN1-OUT1"), true},
			{assign("IN3-OUT2"), true}, {assign("IN4-OUT2"), true}, {take("IN4-OUT2"), true},
			{assign("IN3-OUT3", "IN3-OUT2"), false},
			{take("IN3-OUT2"), true}, {take("IN2-OUT1"), true},
		}},
		{"an IN released from STANDBY goes on air elsewhere", slices.Concat(fourOnOut1, []step{
			{assign("IN2-OUT2", "IN2-OUT1"), true}, {take("IN2-OUT2"), true},
			{take("IN3-OUT1"), true}, {take("IN1-OUT1"), true},
		})},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			s := newSwitching(t, flowFixture(t, ""), true)
			for i, st := range sc.steps {
				if got := s.do(st.op); got != st.accept {
					s.fail("step %d %s: accepted=%v, want %v", i, st.op, got, st.accept)
				}
			}
		})
	}
}

// Random operations, seeded: the model's checks hold after every one. The flow suites use one
// preset and no VVF limit so the oracle decides every refusal; the real suites keep the networks'
// limits and every preset (the oracle is off: VVF shortages are not its business).
func TestSwitchingRandom(t *testing.T) {
	suites := []struct {
		name    string
		fixture func(t *testing.T) *fixture
		presets []string
		oracle  bool
	}{
		{"testbed flow", func(t *testing.T) *fixture { return flowFixture(t, "") }, []string{plain}, true},
		{"testbed cold flow", coldFlowFixture, []string{plain}, true},
		{"rich flow", func(t *testing.T) *fixture { return flowFixture(t, "rich") }, []string{plain}, true},
		{"testbed", newFixture, []string{"preset-1", "preset-2", "preset-3"}, false},
		{"testbed cold standby", newColdFixture, []string{"preset-1", "preset-2", "preset-3"}, false},
	}
	seeds, steps := uint64(6), 40
	if v, err := strconv.ParseUint(os.Getenv("VISFOX_SWITCHING_SEEDS"), 10, 32); err == nil && v > 0 {
		seeds = v // e.g. VISFOX_SWITCHING_SEEDS=200 for a longer search
	}
	for _, suite := range suites {
		for seed := range seeds {
			t.Run(fmt.Sprintf("%s/seed-%d", suite.name, seed), func(t *testing.T) {
				t.Parallel()
				s := newSwitching(t, suite.fixture(t), suite.oracle)
				rng := rand.New(rand.NewPCG(seed, 0x5eed))
				for range steps {
					s.do(s.randomOp(rng, suite.presets))
				}
			})
		}
	}
}

// randomOp picks an operation the cells' states allow, assignments first while few cells are used.
func (s *switching) randomOp(rng *rand.Rand, presets []string) switchOp {
	cells := s.cells()
	ids := slices.Sorted(maps.Keys(cells))
	pick := func(ok func(apitypes.MatrixCell) bool) string {
		var c []string
		for _, id := range ids {
			if ok(cells[id]) {
				c = append(c, id)
			}
		}
		if len(c) == 0 {
			return ""
		}
		return c[rng.IntN(len(c))]
	}
	for {
		switch rng.IntN(6) {
		case 0, 1:
			id := pick(func(c apitypes.MatrixCell) bool { return c.ExecutionState == apitypes.ExecutionStateOff })
			if id == "" {
				continue
			}
			o := switchOp{kind: opAssign, cell: id, preset: presets[rng.IntN(len(presets))]}
			// Half the time accept the releases the IEL offers.
			if rng.IntN(2) == 0 {
				if v, err := s.f.uc.ValidateMatrixAssignment.Run(id, o.preset, nil); err == nil && v.Errors != nil {
					for _, e := range *v.Errors {
						if e.ReleasableCells != nil {
							o.release = append(o.release, *e.ReleasableCells...)
						}
					}
				}
			}
			return o
		case 2, 3:
			if id := pick(func(c apitypes.MatrixCell) bool { return c.ExecutionState == apitypes.ExecutionStateStandby }); id != "" {
				return switchOp{kind: opTake, cell: id}
			}
		case 4:
			if id := pick(func(c apitypes.MatrixCell) bool { return c.ExecutionState == apitypes.ExecutionStateStandby }); id != "" {
				return switchOp{kind: opClear, cell: id}
			}
		case 5:
			if id := pick(func(c apitypes.MatrixCell) bool { return c.ExecutionState == apitypes.ExecutionStateActive }); id != "" {
				return switchOp{kind: opStandby, cell: id}
			}
		}
	}
}
