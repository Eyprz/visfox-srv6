package plan

import (
	"maps"
	"slices"
	"strings"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
)

// This file selects the ways of the streams through the VVFs that control the stream flow (behavior
// select, e.g. a switcher, and replicate, a branch). They form a graph: an IN's headend sends to an
// input port of a flow node, a flow node's output goes to an input port of another flow node, and a
// select node's output goes on to the processing VVFs and the OUT (the root of the OUT's tree).
//
// Capacities (ssot.Limits.Ports, SourceUnicasts):
//   - outputs: a select node sends one stream per OUT it selects for; a replicate node and a
//     headend send one stream per place (edge "node|port"), so OUTs that share an edge share it.
//   - inputs: one per input port in use. A port fed by a select node carries a stream selected for
//     one OUT and serves only that OUT; a port fed by an IN or a replicate node carries the same
//     stream for every OUT that uses it.
//
// Within one OUT's tree every node sends to one place.

// Ways returns every way from an IN through flow nodes to a select node that feeds the OUT
// (upstream first), shortest first.
func (a Selector) Ways(sourceID, destinationID string) [][]domain.Hop {
	var ways [][]domain.Hop
	var walk func(n ssot.FlowNode, after []domain.Hop, seen map[string]bool)
	walk = func(n ssot.FlowNode, after []domain.Hop, seen map[string]bool) {
		if seen[n.ID] {
			return
		}
		for _, in := range n.Inputs {
			way := append([]domain.Hop{{Node: n.ID, Port: in.Port}}, after...)
			if in.From == sourceID {
				ways = append(ways, way)
			}
			if up, ok := a.Resources.Flow(in.From); ok {
				next := maps.Clone(seen)
				next[n.ID] = true
				walk(up, way, next)
			}
		}
	}
	for _, n := range a.roots(destinationID) {
		walk(n, nil, map[string]bool{})
	}
	slices.SortStableFunc(ways, func(x, y []domain.Hop) int { return len(x) - len(y) })
	return ways
}

// roots are the select nodes that can feed the OUT.
func (a Selector) roots(destinationID string) []ssot.FlowNode {
	var out []ssot.FlowNode
	for _, n := range a.Resources.FlowNodes {
		if n.Behavior == domain.BehaviorSelect && slices.Contains(n.Destinations, destinationID) {
			out = append(out, n)
		}
	}
	return out
}

// ledger is what the trees selected so far use, over all OUTs.
type ledger struct {
	// outputs: node id or SourceKey(IN) -> output keys in use -> the OUT that took it first.
	outputs map[string]map[string]string
	// inputs: node id -> port -> the OUTs using it.
	inputs map[string]map[string][]string
}

func newLedger() *ledger {
	return &ledger{outputs: map[string]map[string]string{}, inputs: map[string]map[string][]string{}}
}

// tree is one OUT's ways being built: the ways per IN and where every node sends.
type tree struct {
	out   string
	paths map[string][]domain.Hop
	// down: node id -> "node|port" of the next node, "" for the root (the OUT).
	down map[string]string
	// outputs and inputs this tree adds to the ledger.
	outputs map[string]map[string]bool
	inputs  map[string]map[string]bool
}

func newTree(out string) *tree {
	return &tree{out: out, paths: map[string][]domain.Hop{}, down: map[string]string{},
		outputs: map[string]map[string]bool{}, inputs: map[string]map[string]bool{}}
}

func (t *tree) clone() *tree {
	c := &tree{out: t.out, paths: maps.Clone(t.paths), down: maps.Clone(t.down),
		outputs: map[string]map[string]bool{}, inputs: map[string]map[string]bool{}}
	for k, v := range t.outputs {
		c.outputs[k] = maps.Clone(v)
	}
	for k, v := range t.inputs {
		c.inputs[k] = maps.Clone(v)
	}
	return c
}

// reason is why a way does not fit.
type reason int

const (
	fits reason = iota
	noOutput
	noInput
	noUnicast
	conflict
)

// flow allocates the ways of every OUT (see allocateFeeds).
type flow struct {
	a      Selector
	ledger *ledger
	// ignoreUnicasts checks the flow nodes only (to tell an IN shortage from a flow node shortage).
	ignoreUnicasts bool
}

// capacity of the outputs of a node or IN (0: no limit).
func (f *flow) outputCap(key string) int {
	if strings.HasSuffix(key, "#unicast") {
		if f.ignoreUnicasts {
			return 0
		}
		return f.a.Resources.Limits.SourceUnicasts
	}
	n, _ := f.a.Resources.Flow(key)
	return n.Ports.Outputs
}

// useOutput takes an output key of a node or IN for the tree.
func (f *flow) useOutput(t *tree, owner, key string) reason {
	if _, ok := f.ledger.outputs[owner][key]; ok || t.outputs[owner][key] {
		return fits
	}
	if c := f.outputCap(owner); c > 0 && len(f.ledger.outputs[owner])+len(t.outputs[owner]) >= c {
		if strings.HasSuffix(owner, "#unicast") {
			return noUnicast
		}
		return noOutput
	}
	if t.outputs[owner] == nil {
		t.outputs[owner] = map[string]bool{}
	}
	t.outputs[owner][key] = true
	return fits
}

// useInput takes an input port of a node for the tree. exclusive: the port carries a stream
// selected for this OUT only (fed by a select node).
func (f *flow) useInput(t *tree, node, port string, exclusive bool) reason {
	if t.inputs[node][port] {
		return fits
	}
	if users, ok := f.ledger.inputs[node][port]; ok {
		if exclusive && !slices.Contains(users, t.out) {
			return conflict
		}
		return fits
	}
	n, _ := f.a.Resources.Flow(node)
	used := len(f.ledger.inputs[node]) + len(t.inputs[node])
	if n.Ports.Inputs > 0 && used >= n.Ports.Inputs {
		return noInput
	}
	if t.inputs[node] == nil {
		t.inputs[node] = map[string]bool{}
	}
	t.inputs[node][port] = true
	return fits
}

// add puts the IN's way into the tree. The tree is changed only when the way fits.
func (f *flow) add(t *tree, src string, way []domain.Hop) (*tree, reason) {
	root := way[len(way)-1].Node
	for node, d := range t.down {
		if d == "" && node != root {
			return nil, conflict // one OUT, one root
		}
	}
	next := t.clone()
	if r := f.useOutput(next, SourceKey(src), way[0].Node+"|"+way[0].Port); r != fits {
		return nil, r
	}
	for i, h := range way {
		down := ""
		if i+1 < len(way) {
			down = way[i+1].Node + "|" + way[i+1].Port
		}
		if d, ok := next.down[h.Node]; ok && d != down {
			return nil, conflict
		}
		next.down[h.Node] = down
		n, _ := f.a.Resources.Flow(h.Node)
		key := down
		if n.Behavior == domain.BehaviorSelect {
			key = "out:" + t.out // one output per OUT it selects for
		}
		if r := f.useOutput(next, h.Node, key); r != fits {
			return nil, r
		}
		exclusive := false
		if i > 0 {
			up, _ := f.a.Resources.Flow(way[i-1].Node)
			exclusive = up.Behavior == domain.BehaviorSelect
		}
		if r := f.useInput(next, h.Node, h.Port, exclusive); r != fits {
			return nil, r
		}
	}
	next.paths[src] = way
	return next, fits
}

// commit records the tree in the ledger and the selection's users.
func (f *flow) commit(t *tree, al *Selection) {
	for owner, keys := range t.outputs {
		if f.ledger.outputs[owner] == nil {
			f.ledger.outputs[owner] = map[string]string{}
		}
		for _, key := range slices.Sorted(maps.Keys(keys)) {
			f.ledger.outputs[owner][key] = t.out
			al.Users[owner] = append(al.Users[owner], t.out)
		}
	}
	for node, ports := range t.inputs {
		if f.ledger.inputs[node] == nil {
			f.ledger.inputs[node] = map[string][]string{}
		}
		for _, port := range slices.Sorted(maps.Keys(ports)) {
			f.ledger.inputs[node][port] = append(f.ledger.inputs[node][port], t.out)
			al.Users[InputKey(node)] = append(al.Users[InputKey(node)], t.out)
		}
	}
	// Ports used again by another OUT count once in the ledger but name every OUT using them.
	for node, ports := range f.ledger.inputs {
		for port, users := range ports {
			if !slices.Contains(users, t.out) && usesPort(t, node, port) {
				f.ledger.inputs[node][port] = append(users, t.out)
			}
		}
	}
	// What the tree used is in the ledger now: it may grow from here without counting it twice.
	t.outputs, t.inputs = map[string]map[string]bool{}, map[string]map[string]bool{}
}

func usesPort(t *tree, node, port string) bool {
	for _, way := range t.paths {
		for _, h := range way {
			if h.Node == node && h.Port == port {
				return true
			}
		}
	}
	return false
}

// search extends the tree with a way for every IN in srcs (candidates[i] for srcs[i]), depth first.
// It returns the extended tree, or nil and the reason the last way did not fit.
func (f *flow) search(t *tree, srcs []string, candidates [][][]domain.Hop) (*tree, reason) {
	if len(srcs) == 0 {
		return t, fits
	}
	// The most telling reason: a missing output, else a missing input or IN stream, else a conflict
	// of the ways (they need other nodes).
	why := conflict
	note := func(r reason) {
		if r == noOutput || (r != conflict && why == conflict) {
			why = r
		}
	}
	for _, way := range candidates[0] {
		next, r := f.add(t, srcs[0], way)
		if r != fits {
			note(r)
			continue
		}
		done, r := f.search(next, srcs[1:], candidates[1:])
		if done != nil {
			return done, fits
		}
		note(r)
	}
	return nil, why
}

// How an IN's previous way ranks among its candidates.
type prevRank int

const (
	// prevTie tries the previous way first among equally long ones (a STANDBY when the ways are
	// packed again: which port it waits on does not show).
	prevTie prevRank = iota
	// prevFirst tries the previous way before every other one (the IN being taken: its stream already
	// flows on that way, so the Take only changes what the select nodes pick).
	prevFirst
	// prevKeep allows only the previous way (an on-air IN that is not being taken).
	prevKeep
)

// candidates are the ways an IN may take to the OUT in this tree: those ending at the tree's root
// (any root for an empty tree), the shortest first, with the previous way ranked by rank.
func (a Selector) candidates(t *tree, src, destinationID string, prev []domain.Hop, rank prevRank) [][]domain.Hop {
	root := ""
	for node, d := range t.down {
		if d == "" {
			root = node
		}
	}
	var ways [][]domain.Hop
	for _, w := range a.Ways(src, destinationID) {
		if root == "" || w[len(w)-1].Node == root {
			ways = append(ways, w)
		}
	}
	if len(prev) == 0 {
		return ways
	}
	j := slices.IndexFunc(ways, func(w []domain.Hop) bool { return slices.Equal(w, prev) })
	if rank == prevKeep {
		if j < 0 {
			return nil
		}
		return [][]domain.Hop{ways[j]}
	}
	if j < 0 {
		return ways
	}
	kept := ways[j]
	first := 0
	if rank == prevTie {
		first = slices.IndexFunc(ways, func(w []domain.Hop) bool { return len(w) == len(kept) })
	}
	rest := slices.Delete(slices.Clone(ways), j, j+1)
	return slices.Concat(rest[:first], [][]domain.Hop{kept}, rest[first:])
}

// allocateFeeds gives every OUT the ways of the INs it is sent (domain.SFCRequest.Fed: the on-air IN
// and, with hot standby, the STANDBY INs) through the flow nodes, as one tree per OUT whose root
// select node feeds the OUT. The on-air INs of every OUT are placed first (a STANDBY never takes a
// flow node or IN away from the picture on air), keeping their ways; an IN being taken tries the
// way it is already fed on first (hot standby: the Take only switches). Then the STANDBY INs, the
// OUTs that had a tree first.
//
// Every IN first tries its previous way (stable: the same state selects the same ways, so selecting
// again after a change never moves a feed that was not provisioned). Only when that leaves a
// shortage are the STANDBY ways packed again, shortest first (a STANDBY's way does not show), and
// the packing is kept when it fits more.
func (a Selector) allocateFeeds(streams []domain.SFCRequest, st State, fresh map[string]bool, al *Selection) map[string]map[string][]domain.Hop {
	try := func(pack bool) (map[string]map[string][]domain.Hop, Selection) {
		x := Selection{Users: map[string][]string{}}
		return a.allocateFeedsWith(streams, st, fresh, pack, &x), x
	}
	feeds, x := try(false)
	if len(x.Failed) > 0 {
		if packed, y := try(true); len(y.Failed) < len(x.Failed) {
			feeds, x = packed, y
		}
	}
	al.Failed = append(al.Failed, x.Failed...)
	for k, v := range x.Users {
		al.Users[k] = append(al.Users[k], v...)
	}
	return feeds
}

// allocateFeedsWith is allocateFeeds keeping the previous ways (pack false) or packing the STANDBY
// ways again (pack true).
// A shortage is a failure of the IN unicast (source_unicast) and / or of a flow node output or input
// (vvf_output / vvf_input): both are reported when both are short. Without a free way a request
// fails when the domain policy requires a select node; the INs that fit keep their ways.
func (a Selector) allocateFeedsWith(streams []domain.SFCRequest, st State, fresh map[string]bool, pack bool, al *Selection) map[string]map[string][]domain.Hop {
	hot := a.Resources.Limits.HotStandby()
	f := &flow{a: a, ledger: newLedger()}
	trees := map[string]*tree{}
	prevWay := func(s domain.SFCRequest, src string) []domain.Hop {
		prev := st.Previous[s.DestinationID]
		if w, ok := prev.Feeds[src]; ok {
			return w
		}
		if prev.Feeds == nil && s.OnAir && s.SourceID == src {
			return prev.Way
		}
		return nil
	}
	direct := len(a.Resources.FlowNodes) == 0 || !a.hasRoots()
	rank := func(s domain.SFCRequest, src string) prevRank {
		switch {
		case !s.OnAir || src != s.SourceID:
			if pack {
				return prevTie
			}
			return prevFirst
		case fresh[s.DestinationID]:
			return prevFirst
		default:
			return prevKeep
		}
	}

	// place adds the INs to the OUT's tree; on a shortage it records why and adds the INs that fit
	// one by one.
	place := func(s domain.SFCRequest, srcs []string) {
		if len(srcs) == 0 {
			return
		}
		t := trees[s.DestinationID]
		if t == nil {
			t = newTree(s.DestinationID)
		}
		if direct {
			// No select node: each IN goes straight to the processing VVFs (one unicast per OUT).
			for _, src := range srcs {
				if r := f.useOutput(t, SourceKey(src), "out:"+s.DestinationID); r != fits {
					al.Failed = append(al.Failed, Failure{DestinationID: s.DestinationID, Name: src, Pool: ssot.PoolSourceUnicast, SourceID: src})
					continue
				}
				t.paths[src] = nil
			}
			f.commit(t, al)
			trees[s.DestinationID] = t
			return
		}
		cands := func(t *tree) [][][]domain.Hop {
			out := make([][][]domain.Hop, len(srcs))
			for i, src := range srcs {
				out[i] = a.candidates(t, src, s.DestinationID, prevWay(s, src), rank(s, src))
			}
			return out
		}
		if done, _ := f.search(t, srcs, cands(t)); done != nil {
			f.commit(done, al)
			trees[s.DestinationID] = done
			return
		}
		a.shortage(f, t, s, srcs, cands(t), al)
		// Keep what fits, IN by IN.
		for _, src := range srcs {
			one := []string{src}
			c := [][][]domain.Hop{a.candidates(t, src, s.DestinationID, prevWay(s, src), rank(s, src))}
			if next, r := f.search(t, one, c); r == fits {
				t = next
			}
		}
		f.commit(t, al)
		trees[s.DestinationID] = t
	}

	// The on-air INs first: those that stay on air keep their ways before those being taken.
	for _, taking := range []bool{false, true} {
		for _, s := range streams {
			if s.OnAir && fresh[s.DestinationID] == taking {
				place(s, []string{s.SourceID})
			}
		}
	}
	// Then the STANDBY INs, the OUTs that had a tree first.
	order := slices.Clone(streams)
	had := func(s domain.SFCRequest) int {
		if p := st.Previous[s.DestinationID]; len(p.Feeds)+len(p.Way) > 0 {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(order, func(x, y domain.SFCRequest) int { return had(x) - had(y) })
	for _, s := range order {
		var srcs []string
		for _, src := range s.Fed(hot) {
			if !s.OnAir || src != s.SourceID {
				srcs = append(srcs, src)
			}
		}
		// The INs it had first.
		slices.SortStableFunc(srcs, func(x, y string) int {
			return boolInt(prevWay(s, x) == nil) - boolInt(prevWay(s, y) == nil)
		})
		place(s, srcs)
	}

	result := map[string]map[string][]domain.Hop{}
	for out, t := range trees {
		if len(t.paths) > 0 {
			result[out] = t.paths
		}
	}
	return result
}

func (a Selector) hasRoots() bool {
	return slices.ContainsFunc(a.Resources.FlowNodes, func(n ssot.FlowNode) bool {
		return n.Behavior == domain.BehaviorSelect && len(n.Destinations) > 0
	})
}

// shortage records why the INs do not fit the OUT's tree: the INs whose headend has no stream left
// (source_unicast), and the flow nodes (vvf_output / vvf_input) when the ways do not fit even with
// every headend free.
func (a Selector) shortage(f *flow, t *tree, s domain.SFCRequest, srcs []string, cands [][][]domain.Hop, al *Selection) {
	free := &flow{a: a, ledger: f.ledger, ignoreUnicasts: true}
	done, why := free.search(t, srcs, cands)
	if done == nil && a.SwitcherRequired {
		pool, vvf, name := ssot.PoolVVFOutput, "", "Switcher"
		if why == noInput {
			pool = ssot.PoolVVFInput
		}
		if roots := a.roots(s.DestinationID); len(roots) > 0 {
			vvf, name = roots[0].VVFID, roots[0].VVFName
		}
		al.Failed = append(al.Failed, Failure{DestinationID: s.DestinationID, VVFID: vvf, Name: name, Pool: pool})
	}
	// The INs short of unicasts: when the flow nodes fit, those whose way in that solution needs a new
	// stream from a headend that sends as many as it can; else those none of whose ways reuses a
	// stream the headend already sends.
	limit := a.Resources.Limits.SourceUnicasts
	for i, src := range srcs {
		key := SourceKey(src)
		if limit <= 0 || len(f.ledger.outputs[key])+len(t.outputs[key]) < limit {
			continue
		}
		reused := func(w []domain.Hop) bool {
			_, ok := f.ledger.outputs[key][w[0].Node+"|"+w[0].Port]
			return ok || t.outputs[key][w[0].Node+"|"+w[0].Port]
		}
		short := !slices.ContainsFunc(cands[i], reused)
		if done != nil {
			short = !reused(done.paths[src])
		}
		if short {
			al.Failed = append(al.Failed, Failure{DestinationID: s.DestinationID, Name: src, Pool: ssot.PoolSourceUnicast, SourceID: src})
		}
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
