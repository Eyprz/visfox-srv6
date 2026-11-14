package ssot

import (
	"fmt"
	"slices"
	"sort"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// Wiring is the physical wiring of the input ports of a VVF node that controls the stream flow
// (behavior select or replicate, e.g. a switcher) on the testbed (network operator config):
// input port -> IN id or the host name of the upstream node.
// v2 derives it from the BGP-LS links.
type Wiring struct {
	// Node is the advertised host name of the node.
	Node   string            `yaml:"node"`
	Inputs map[string]string `yaml:"inputs"`
	// Mode is how a select node selects a stream: "filter" (default: it passes the selected stream by
	// its SIDs, every stream carries its whole path) or "swap" (it rewrites the selected stream's
	// header with the main stream's, the current testbed).
	Mode string `yaml:"mode"`
}

// Ports are how many streams a VVF node that controls the stream flow receives and sends at once.
// Zero means no limit.
type Ports struct {
	// Inputs are the streams it receives at once (input ports in use; e.g. NIC bandwidth).
	Inputs int `yaml:"inputs"`
	// Outputs are the streams it sends at once: per OUT a select node sends to, per place a
	// replicate node sends a copy to.
	Outputs int `yaml:"outputs"`
}

// Limits are the resource pool capacities that the TED does not advertise (network operator config).
// Zero means no limit.
type Limits struct {
	// VVFStreams is the number of streams one node of a processing VVF processes at once (VVF id ->
	// streams).
	VVFStreams map[string]int `yaml:"vvf_streams"`
	// Ports are the inputs and outputs of the VVFs that control the stream flow (VVF id -> ports).
	Ports map[string]Ports `yaml:"ports"`
	// SourceUnicasts is the number of streams an IN's headend sends at once: one per first VVF input
	// it sends to (1 without a branch: one OUT).
	SourceUnicasts int `yaml:"source_unicasts"`
	// StandbyFeed is how a STANDBY stream waits for its Take: StandbyFeedNone (default) holds only
	// its VVF nodes and the Take sets up the whole path; StandbyFeedSwitcher (hot standby) already
	// sends it into a select VVF (switcher) that feeds the OUT, so the Take only switches. With
	// StandbyFeedSwitcher the flow VVF and IN unicast pools are reserved at STANDBY.
	StandbyFeed string `yaml:"standby_feed"`
}

// Standby feeds (Limits.StandbyFeed).
const (
	StandbyFeedNone     = "none"
	StandbyFeedSwitcher = "switcher"
)

// HotStandby reports whether STANDBY streams are already sent into their switchers.
func (l Limits) HotStandby() bool { return l.StandbyFeed == StandbyFeedSwitcher }

// FeedReservedAt is when the flow VVF (vvf_input / vvf_output) and IN unicast pools are reserved.
func (l Limits) FeedReservedAt() apitypes.ResourcePoolReservedAt {
	if l.HotStandby() {
		return apitypes.ResourcePoolReservedAtStandby
	}
	return apitypes.ResourcePoolReservedAtOnAir
}

// Resource pool kinds.
const (
	PoolVVFNode       = "vvf_node"
	PoolVVFOutput     = "vvf_output"
	PoolVVFInput      = "vvf_input"
	PoolSourceUnicast = "source_unicast"
)

// FlowNode is a VVF node that controls the stream flow (behavior select or replicate: a switcher, a
// branch) with its wired input ports and capacity.
type FlowNode struct {
	ID    string
	Label string
	VVFID string
	// VVFName is the VVF's name in the catalog (e.g. "Switcher").
	VVFName  string
	Behavior domain.Behavior
	// Mode is how a select node selects ("filter" / "swap").
	Mode string
	// Inputs are the wired input ports: port -> IN id or upstream flow node id. Ports not wired
	// are not offered.
	Inputs []apitypes.SwitcherInput
	Ports  Ports
	// Destinations are the OUTs a select node's output can reach after the processing VVFs (all:
	// one SRv6 egress); empty for replicate nodes, which feed others.
	Destinations []string
}

// Resources is the TED as the northbound API shows it: IN / OUT, the VVF nodes that process streams
// and those that control their flow with their wiring, and the resource pools with their capacities.
type Resources struct {
	TED          domain.TED
	Sources      []apitypes.SourceNode
	Destinations []apitypes.DestinationNode
	// Switchers are the select nodes as the API shows them (GET /resources/switchers).
	Switchers []apitypes.SwitcherNode
	// VVFNodes are the nodes of processing VVFs (behavior process), in id order.
	VVFNodes []domain.Node
	// FlowNodes are the nodes of VVFs that control the stream flow (select, replicate), in id order.
	FlowNodes []FlowNode
	Limits    Limits

	nodes map[string]domain.Node
	flow  map[string]int
}

// NewResources builds the resources from the interpreted TED, the VVF definitions (catalog: their
// behavior and name), and the network operator config. A VVF missing from the definitions processes
// streams.
func NewResources(t domain.TED, vvfs []apitypes.VvfDefinition, wiring []Wiring, limits Limits) (*Resources, error) {
	behaviors, names := map[string]domain.Behavior{}, map[string]string{}
	for _, v := range vvfs {
		behaviors[v.Id], names[v.Id] = domain.BehaviorProcess, v.Name
		if v.Behavior != nil {
			behaviors[v.Id] = domain.Behavior(*v.Behavior)
		}
	}
	switch limits.StandbyFeed {
	case "", StandbyFeedNone, StandbyFeedSwitcher:
	default:
		return nil, fmt.Errorf("limits: unknown standby_feed %q", limits.StandbyFeed)
	}
	r := &Resources{TED: t, Limits: limits, nodes: map[string]domain.Node{}, flow: map[string]int{}}
	behavior := func(n domain.Node) domain.Behavior {
		if b, ok := behaviors[n.VVFID]; ok && b != "" {
			return b
		}
		return domain.BehaviorProcess
	}
	byHost := map[string]domain.Node{}
	var flow []domain.Node
	for _, n := range t.Nodes {
		r.nodes[n.ID] = n
		byHost[n.Hostname] = n
		if n.Role != domain.RoleVVF {
			continue
		}
		if behavior(n) == domain.BehaviorProcess {
			r.VVFNodes = append(r.VVFNodes, n)
		} else {
			flow = append(flow, n)
		}
	}
	sort.Slice(r.VVFNodes, func(i, j int) bool { return r.VVFNodes[i].ID < r.VVFNodes[j].ID })
	sort.Slice(flow, func(i, j int) bool { return flow[i].ID < flow[j].ID })
	for vvf := range limits.Ports {
		if b, ok := behaviors[vvf]; !ok || b == domain.BehaviorProcess {
			return nil, fmt.Errorf("limits.ports: %s is not a VVF that controls the stream flow", vvf)
		}
	}

	sources := map[string]bool{}
	for _, e := range t.Sources {
		sources[e.ID] = true
		r.Sources = append(r.Sources, apitypes.SourceNode{
			Id: e.ID, Name: e.Name, Status: apitypes.SourceNodeStatusOnline, VideoFormat: apitypes.VideoFormat(e.VideoFormat),
		})
	}
	destinationIDs := []string{}
	for _, e := range t.Destinations {
		destinationIDs = append(destinationIDs, e.ID)
		r.Destinations = append(r.Destinations, apitypes.DestinationNode{
			Id: e.ID, Name: e.Name, Status: apitypes.DestinationNodeStatusOnline, VideoFormat: apitypes.VideoFormat(e.VideoFormat),
		})
	}

	isFlow := func(n domain.Node) bool { return slices.ContainsFunc(flow, func(f domain.Node) bool { return f.ID == n.ID }) }
	wired := map[string]Wiring{}
	for _, w := range wiring {
		n, ok := byHost[w.Node]
		if !ok || !isFlow(n) {
			return nil, fmt.Errorf("wiring: %q is not a node of a VVF that controls the stream flow (select / replicate)", w.Node)
		}
		switch w.Mode {
		case "", "filter", "swap":
		default:
			return nil, fmt.Errorf("wiring %s: unknown mode %q", w.Node, w.Mode)
		}
		if w.Mode != "" && behavior(n) != domain.BehaviorSelect {
			return nil, fmt.Errorf("wiring %s: only select nodes have a mode", w.Node)
		}
		wired[n.ID] = w
	}
	for _, n := range flow {
		w := wired[n.ID]
		fn := FlowNode{ID: n.ID, Label: n.Label, VVFID: n.VVFID, VVFName: names[n.VVFID], Behavior: behavior(n), Mode: "filter",
			Inputs: []apitypes.SwitcherInput{}, Ports: limits.Ports[n.VVFID]}
		if w.Mode != "" {
			fn.Mode = w.Mode
		}
		if fn.Behavior == domain.BehaviorSelect {
			fn.Destinations = slices.Clone(destinationIDs)
		}
		for _, in := range n.Inputs {
			from, ok := w.Inputs[in.Port]
			if !ok {
				continue // not wired: the port is not offered
			}
			switch up, known := byHost[from]; {
			case sources[from]:
			case known && isFlow(up) && up.ID != n.ID:
				from = up.ID
			default:
				return nil, fmt.Errorf("wiring %s port %s: %q is neither an IN nor another select / replicate node", w.Node, in.Port, from)
			}
			fn.Inputs = append(fn.Inputs, apitypes.SwitcherInput{Port: in.Port, From: from})
		}
		for port := range w.Inputs {
			if !slices.ContainsFunc(n.Inputs, func(in domain.InputPort) bool { return in.Port == port }) {
				return nil, fmt.Errorf("wiring %s: port %s has no input SID in the TED", w.Node, port)
			}
		}
		r.flow[n.ID] = len(r.FlowNodes)
		r.FlowNodes = append(r.FlowNodes, fn)
		if fn.Behavior == domain.BehaviorSelect {
			sw := apitypes.SwitcherNode{Id: n.ID, Label: n.Label, Inputs: fn.Inputs, DestinationIds: fn.Destinations}
			if n.RouterID != "" {
				sw.RouterId = ptr(n.RouterID)
			}
			r.Switchers = append(r.Switchers, sw)
		}
	}
	return r, nil
}

// Flow returns a node of a VVF that controls the stream flow.
func (r *Resources) Flow(id string) (FlowNode, bool) {
	i, ok := r.flow[id]
	if !ok {
		return FlowNode{}, false
	}
	return r.FlowNodes[i], true
}

// Mode is how a select node selects ("filter" when it is not one).
func (r *Resources) Mode(id string) string {
	if f, ok := r.Flow(id); ok {
		return f.Mode
	}
	return "filter"
}

// Node returns a TED node by id.
func (r *Resources) Node(id string) (domain.Node, bool) {
	n, ok := r.nodes[id]
	return n, ok
}

// Source returns an IN with its headend binding.
func (r *Resources) Source(id string) (domain.Endpoint, bool) {
	i := slices.IndexFunc(r.TED.Sources, func(e domain.Endpoint) bool { return e.ID == id })
	if i < 0 {
		return domain.Endpoint{}, false
	}
	return r.TED.Sources[i], true
}

// Destination returns an OUT with its decap binding.
func (r *Resources) Destination(id string) (domain.Endpoint, bool) {
	i := slices.IndexFunc(r.TED.Destinations, func(e domain.Endpoint) bool { return e.ID == id })
	if i < 0 {
		return domain.Endpoint{}, false
	}
	return r.TED.Destinations[i], true
}

// Pools returns every resource pool with its capacity. usedBy reports the streams using a pool.
func (r *Resources) Pools(usedBy func(kind, nodeID string) []apitypes.PoolUse) []apitypes.ResourcePool {
	pools := []apitypes.ResourcePool{}
	add := func(kind, nodeID, label string, group *string, capacity int, sharing apitypes.ResourcePoolSharing,
		at apitypes.ResourcePoolReservedAt) {
		p := apitypes.ResourcePool{
			Id: kind + ":" + nodeID, Kind: kind, NodeId: nodeID, Group: group, Label: label,
			Sharing: sharing, ReservedAt: at, UsedBy: usedBy(kind, nodeID),
		}
		if capacity > 0 {
			p.Capacity = ptr(capacity)
		}
		if p.UsedBy == nil {
			p.UsedBy = []apitypes.PoolUse{}
		}
		pools = append(pools, p)
	}
	for _, n := range r.VVFNodes {
		add(PoolVVFNode, n.ID, n.Label, ptr(n.VVFID), r.Limits.VVFStreams[n.VVFID],
			apitypes.ResourcePoolSharingSameConfig, apitypes.ResourcePoolReservedAtStandby)
	}
	at := r.Limits.FeedReservedAt()
	for _, f := range r.FlowNodes {
		add(PoolVVFOutput, f.ID, f.Label+" output", ptr(f.VVFID), f.Ports.Outputs, apitypes.ResourcePoolSharingNone, at)
		add(PoolVVFInput, f.ID, f.Label+" inputs", ptr(f.VVFID), f.Ports.Inputs, apitypes.ResourcePoolSharingNone, at)
	}
	for _, s := range r.Sources {
		add(PoolSourceUnicast, s.Id, s.Name+" unicast", nil, r.Limits.SourceUnicasts, apitypes.ResourcePoolSharingNone, at)
	}
	return pools
}

func ptr[T any](v T) *T { return &v }
