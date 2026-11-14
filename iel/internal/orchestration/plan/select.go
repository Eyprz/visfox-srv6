// Package plan is the plan step of Intent Orchestration (RFC 9315 §5.1.3, learn / plan): it checks
// that the SFC requests fit the resource pools of the SSoT (SFC Validator, resource part) and selects
// a Service Function Path for each (RFC 7665): the processing VVF nodes of its SF instances and the
// way of its INs through the VVFs that control the stream flow (switchers, branches: flow.go),
// keeping current paths and making the new one before breaking the old. It
// follows the MSW mock (application/src/mocks/db.ts allocateNodes / allocateFeeds).
package plan

import (
	"fmt"
	"slices"
	"strings"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
)

// Failure is an SF instance of a request without a free VVF node, or a request whose INs cannot
// reach the OUT through free flow VVF ports or IN unicasts.
type Failure struct {
	DestinationID string
	VVFID         string
	// Name names the VVF (the pinned node's label for a pinned block).
	Name string
	// NodeID is the pinned node of a pinned block.
	NodeID string
	// Pool is the kind of the pool that ran out when it is not a processing VVF node
	// (ssot.PoolVVFOutput, ssot.PoolVVFInput, ssot.PoolSourceUnicast).
	Pool string
	// SourceID is the IN of a source_unicast shortage.
	SourceID string
}

// Selection is the SFP of every request (by destination id) and what did not fit.
type Selection struct {
	SFPs   map[string]domain.SFP
	Failed []Failure
	// Users lists the streams (destination ids) using each processing VVF node, flow VVF output
	// (node id: once per output), flow VVF input (InputKey: once per input port) and IN unicast
	// (SourceKey: once per stream the headend sends).
	Users map[string][]string
}

// InputKey is the Selection.Users key of a flow VVF node's inputs.
func InputKey(nodeID string) string { return nodeID + "#in" }

// SourceKey is the Selection.Users key of an IN's unicasts.
func SourceKey(sourceID string) string { return sourceID + "#unicast" }

// State is the SFPs the requests have now and the config every VVF node runs now (node id ->
// SFInstance.Config): a Take injects the on-air intent's config right before the switch.
type State struct {
	Previous   map[string]domain.SFP
	Configured map[string]string
}

// Selector selects SFPs on the resource pools of the TED.
type Selector struct {
	Resources *ssot.Resources
	// SwitcherRequired: every stream passes a select VVF (a switcher; domain policy).
	SwitcherRequired bool
}

// Select assigns every request's SF instances to TED nodes and every on-air request to switchers.
//
// A node runs one config: streams whose block config is equal share it up to the VVF's stream
// limit, any other config needs a free node. Streams keep their current nodes when still valid,
// then the rest prefer nodes already running the same config before taking a free one. One stream
// never uses a node twice, and a pinned block uses only its node.
//
// fresh names the streams being taken (destination ids): their blocks avoid nodes that run another
// config now, so the new config is injected before the switch without touching the picture on air
// (make-before-break). Only when no such node is free do they reuse one.
func (a Selector) Select(streams []domain.SFCRequest, st State, fresh map[string]bool) Selection {
	al := Selection{SFPs: map[string]domain.SFP{}, Users: map[string][]string{}}
	feeds := a.allocateFeeds(streams, st, fresh, &al)

	type load struct {
		config  string
		streams int
	}
	loads := map[string]load{}
	used := map[string]map[string]bool{}
	limit := a.Resources.Limits.VVFStreams
	busyElse := func(key, nodeID, config string) bool {
		live, ok := st.Configured[nodeID]
		return fresh[key] && ok && live != config
	}
	fits := func(nodeID, vvfID, config string) bool {
		l, ok := loads[nodeID]
		return !ok || (l.config == config && (limit[vvfID] <= 0 || l.streams < limit[vvfID]))
	}
	blocks := make([][]domain.SFInstance, len(streams))
	nodes := make([][]string, len(streams))
	put := func(i, b int, nodeID, config string) {
		key := streams[i].DestinationID
		l := loads[nodeID]
		loads[nodeID] = load{config: config, streams: l.streams + 1}
		al.Users[nodeID] = append(al.Users[nodeID], key)
		nodes[i][b] = nodeID
		used[key][nodeID] = true
	}
	candidates := func(b domain.SFInstance) []string {
		var ids []string
		for _, n := range a.Resources.VVFNodes {
			if n.VVFID == b.VVF.Id && (b.Pinned == "" || n.ID == b.Pinned) {
				ids = append(ids, n.ID)
			}
		}
		return ids
	}

	// Keep the current nodes first.
	for i, s := range streams {
		blocks[i] = s.SFs
		nodes[i] = make([]string, len(blocks[i]))
		used[s.DestinationID] = map[string]bool{}
		previous := st.Previous[s.DestinationID].Nodes
		for b, block := range blocks[i] {
			if b >= len(previous) || previous[b] == "" {
				continue
			}
			id, config := previous[b], block.Config()
			if slices.Contains(candidates(block), id) && !used[s.DestinationID][id] &&
				fits(id, block.VVF.Id, config) && !busyElse(s.DestinationID, id, config) {
				put(i, b, id, config)
			}
		}
	}
	// Then nodes running the same config, then free ones.
	for i, s := range streams {
		for b, block := range blocks[i] {
			if nodes[i][b] != "" {
				continue
			}
			config := block.Config()
			var all, quiet []string
			for _, id := range candidates(block) {
				if used[s.DestinationID][id] || !fits(id, block.VVF.Id, config) {
					continue
				}
				all = append(all, id)
				if !busyElse(s.DestinationID, id, config) {
					quiet = append(quiet, id)
				}
			}
			pick := quiet
			if len(pick) == 0 {
				pick = all
			}
			if len(pick) == 0 {
				name := block.VVF.Name
				if n, ok := a.Resources.Node(block.Pinned); ok {
					name = n.Label
				}
				al.Failed = append(al.Failed, Failure{DestinationID: s.DestinationID, VVFID: block.VVF.Id, Name: name, NodeID: block.Pinned})
				continue
			}
			id := pick[0]
			if j := slices.IndexFunc(pick, func(id string) bool { _, ok := loads[id]; return ok }); j >= 0 {
				id = pick[j]
			}
			put(i, b, id, config)
		}
	}
	for i, s := range streams {
		sfp := domain.SFP{Nodes: nodes[i], Feeds: feeds[s.DestinationID]}
		if s.OnAir {
			sfp.Way = feeds[s.DestinationID][s.SourceID]
		}
		al.SFPs[s.DestinationID] = sfp
	}
	return al
}

// Feasibility is the first issue of the selection (nil when everything fits).
func (a Selector) Feasibility(al Selection) *apitypes.ValidationIssue {
	issues := a.Issues(al, "")
	if len(issues) == 0 {
		return nil
	}
	return &issues[0]
}

// Issues is the resource part of the SFC Validator: every shortage of the selection as a
// RESOURCE_EXHAUSTED validation issue naming the nodes that ran out (bottleneck), one per OUT and
// pool. The shortages of focus (the OUT a change is for) come first, its VVF nodes and switchers
// before its IN unicasts (what a release of STANDBY cells may not solve); on the other OUTs the IN
// unicasts come first (the change took an IN they had, and their switchers follow from it).
func (a Selector) Issues(al Selection, focus string) []apitypes.ValidationIssue {
	rank := func(f Failure) int {
		unicast := f.Pool == ssot.PoolSourceUnicast
		switch {
		case f.DestinationID == focus && !unicast:
			return 0
		case f.DestinationID == focus:
			return 1
		case unicast:
			return 2
		}
		return 3
	}
	failed := slices.Clone(al.Failed)
	slices.SortStableFunc(failed, func(x, y Failure) int { return rank(x) - rank(y) })
	var out []apitypes.ValidationIssue
	seen := map[string]bool{}
	for _, f := range failed {
		key := f.DestinationID + "|" + f.Pool + "|" + f.VVFID + "|" + f.NodeID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a.issue(f, al))
	}
	return out
}

func (a Selector) issue(f Failure, al Selection) apitypes.ValidationIssue {
	kind, what := ssot.PoolVVFNode, f.Name+" VVF node"
	type ref struct{ id, key, label string }
	var pool []ref
	switch f.Pool {
	case ssot.PoolSourceUnicast:
		kind = f.Pool
		label := f.SourceID
		if i := slices.IndexFunc(a.Resources.Sources, func(s apitypes.SourceNode) bool { return s.Id == f.SourceID }); i >= 0 {
			label = a.Resources.Sources[i].Name
		}
		pool = append(pool, ref{f.SourceID, SourceKey(f.SourceID), label})
	case ssot.PoolVVFOutput, ssot.PoolVVFInput:
		kind, what = f.Pool, f.Name+" output"
		if f.Pool == ssot.PoolVVFInput {
			what = f.Name + " input"
		}
		for _, n := range a.Resources.FlowNodes {
			if f.VVFID != "" && n.VVFID != f.VVFID {
				continue
			}
			key := n.ID
			if f.Pool == ssot.PoolVVFInput {
				key = InputKey(n.ID)
			}
			pool = append(pool, ref{n.ID, key, n.Label})
		}
	default:
		// A pinned block can only use its own node, so that node is the whole bottleneck.
		for _, n := range a.Resources.VVFNodes {
			if (f.NodeID != "" && n.ID == f.NodeID) || (f.NodeID == "" && n.VVFID == f.VVFID) {
				pool = append(pool, ref{n.ID, n.ID, n.Label})
			}
		}
	}
	nodes := make([]apitypes.BottleneckNode, 0, len(pool))
	var users []string
	for _, n := range pool {
		others := []string{}
		for _, d := range al.Users[n.key] {
			if d != f.DestinationID && !slices.Contains(others, d) {
				others = append(others, d)
			}
		}
		users = append(users, others...)
		nodes = append(nodes, apitypes.BottleneckNode{NodeId: n.id, Label: n.label, DestinationIds: others})
	}
	message := fmt.Sprintf("No free %s for the stream to %s", what, f.DestinationID)
	if kind == ssot.PoolSourceUnicast {
		slices.Sort(users)
		message = fmt.Sprintf("%s is already sent to %s", f.SourceID, strings.Join(slices.Compact(users), ", "))
	}
	return apitypes.ValidationIssue{
		Code: string(apitypes.ErrorCodeRESOURCEEXHAUSTED), ResourceKind: &kind,
		Message: message, BottleneckNodes: &nodes,
	}
}
