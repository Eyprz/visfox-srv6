// Package render is the render step of Intent Orchestration (RFC 9315 §5.1.3, plan / render): it
// turns the selected Service Function Path of an SFC request into southbound instructions the
// provisioning step runs: SR Policies (RFC 9256: headend, color, endpoint, Segment List with the
// RFC 8986 arguments), switcher selections and VVF configs, in the order a Take needs, and
// reconciles them with the OUT's previous stream.
package render

import (
	"encoding/binary"
	"fmt"
	"maps"
	"net/netip"
	"slices"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
)

// Switcher modes (network operator config).
const (
	// SwitcherFilter passes the selected stream by its SIDs: every stream carries its whole path.
	SwitcherFilter = "filter"
	// SwitcherSwap rewrites the selected sub stream's header with the main stream's (the stream on
	// the input with next 0): a sub stream carries its path up to the switcher only, and the path
	// after the switcher is the main stream's (current testbed, D4S).
	SwitcherSwap = "swap"
)

// ARG rules: what the ARG block of the SIDs carries (SID interpretation rule).
const (
	// ArgNone leaves the arguments empty.
	ArgNone = "none"
	// ArgStream puts the IN's argument on every SID of its path (current testbed: `fd77:XY::N`).
	ArgStream = "stream"
	// ArgEgress puts the OUT's argument on the egress SID: it identifies the terminating node.
	ArgEgress = "egress"
)

// Renderer renders SFPs into instructions.
type Renderer struct {
	Resources *ssot.Resources
	// Arg is the ARG rule (ArgNone when empty).
	Arg string
	// ColorBase + the OUT's position (1-based) is the SR Policy color; 100 when zero.
	ColorBase uint32
}

// Leg is one SR Policy of a request: the headend of Source sends Segments to Endpoint.
type Leg struct {
	Source   string
	Endpoint string
	Segments []string
	// Carrier marks the main stream of a swap switcher carrying the path after the switcher.
	Carrier bool
}

// Legs returns the SR Policies a stream on the given switchers and VVF nodes needs. With filter
// switchers (or none) it is one policy over the whole path. At a swap switcher the stream entering
// on a sub input ends there, and the switcher's main stream (input with next 0) carries the rest.
func (p Renderer) Legs(s domain.SFCRequest, al domain.SFP) ([]Leg, error) {
	var tail []string
	for _, id := range al.Nodes {
		n, ok := p.Resources.Node(id)
		if !ok || len(n.SIDs) == 0 {
			return nil, fmt.Errorf("stream to %s: no VVF node for every block", s.DestinationID)
		}
		tail = append(tail, n.SIDs[0])
	}
	dst, ok := p.Resources.Destination(s.DestinationID)
	if !ok {
		return nil, fmt.Errorf("unknown destination %s", s.DestinationID)
	}
	decap, ok := p.Resources.Node(dst.NodeID)
	if !ok || len(decap.SIDs) == 0 {
		return nil, fmt.Errorf("%s: the decap node advertises no SID", s.DestinationID)
	}
	tail = append(tail, decap.SIDs[0])

	var legs []Leg
	var build func(source string, hops []domain.Hop, carrier bool) error
	build = func(source string, hops []domain.Hop, carrier bool) error {
		var segs []string
		for i, h := range hops {
			in, err := p.Input(h)
			if err != nil {
				return err
			}
			segs = append(segs, in.SID)
			if f, _ := p.Resources.Flow(h.Node); f.Behavior == domain.BehaviorReplicate {
				// RFC 9524 replication segments are not rendered yet.
				return fmt.Errorf("stream to %s: %s replicates the stream, which cannot be rendered yet", s.DestinationID, h.Node)
			}
			if p.Resources.Mode(h.Node) != SwitcherSwap || in.Next == 0 {
				continue
			}
			legs = append(legs, Leg{Source: source, Endpoint: h.Node, Segments: segs, Carrier: carrier})
			main, port, err := p.mainSource(h.Node)
			if err != nil {
				return err
			}
			return build(main, append([]domain.Hop{{Node: h.Node, Port: port}}, hops[i+1:]...), true)
		}
		legs = append(legs, Leg{Source: source, Endpoint: decap.ID, Segments: append(segs, tail...), Carrier: carrier})
		return nil
	}
	if err := build(s.SourceID, al.Way, false); err != nil {
		return nil, err
	}
	for i := range legs {
		args, err := p.withArgs(legs[i], s.DestinationID)
		if err != nil {
			return nil, err
		}
		legs[i].Segments = args
	}
	return legs, nil
}

// Input returns the input port a hop enters on.
func (p Renderer) Input(h domain.Hop) (domain.InputPort, error) {
	n, ok := p.Resources.Node(h.Node)
	if ok {
		if i := slices.IndexFunc(n.Inputs, func(in domain.InputPort) bool { return in.Port == h.Port }); i >= 0 {
			return n.Inputs[i], nil
		}
	}
	return domain.InputPort{}, fmt.Errorf("%s has no input %s", h.Node, h.Port)
}

// mainSource is the IN wired to the swap switcher's main input (next 0).
func (p Renderer) mainSource(switcherID string) (source, port string, err error) {
	n, _ := p.Resources.Node(switcherID)
	i := slices.IndexFunc(n.Inputs, func(in domain.InputPort) bool { return in.Next == 0 })
	if i < 0 {
		return "", "", fmt.Errorf("swap switcher %s has no main input", switcherID)
	}
	port = n.Inputs[i].Port
	if sw, ok := p.Resources.Flow(switcherID); ok {
		for _, in := range sw.Inputs {
			if in.Port != port {
				continue
			}
			if _, ok := p.Resources.Source(in.From); !ok {
				return "", "", fmt.Errorf("swap switcher %s: the main input %s must be an IN, not %s", switcherID, port, in.From)
			}
			return in.From, port, nil
		}
	}
	return "", "", fmt.Errorf("swap switcher %s: the main input %s is not wired", switcherID, port)
}

func (p Renderer) withArgs(l Leg, destinationID string) ([]string, error) {
	out := slices.Clone(l.Segments)
	switch p.Arg {
	case "", ArgNone:
		return out, nil
	case ArgStream:
		src, _ := p.Resources.Source(l.Source)
		for i, sid := range out {
			s, err := WithArg(sid, src.Arg)
			if err != nil {
				return nil, err
			}
			out[i] = s
		}
	case ArgEgress:
		// Only a leg that ends at the decap reaches the terminating node.
		if n, ok := p.Resources.Node(l.Endpoint); ok && n.Role == domain.RoleDecap {
			dst, _ := p.Resources.Destination(destinationID)
			s, err := WithArg(out[len(out)-1], dst.Arg)
			if err != nil {
				return nil, err
			}
			out[len(out)-1] = s
		}
	default:
		return nil, fmt.Errorf("unknown ARG rule %q", p.Arg)
	}
	return out, nil
}

// WithArg puts an argument into the low-order bits of a SID (the ARG block ends the SID, RFC 8986).
// The SID must have those bits clear.
func WithArg(sid string, arg uint64) (string, error) {
	if arg == 0 {
		return sid, nil
	}
	a, err := netip.ParseAddr(sid)
	if err != nil || !a.Is6() {
		return "", fmt.Errorf("SID %q is not an IPv6 address", sid)
	}
	b := a.As16()
	low := binary.BigEndian.Uint64(b[8:])
	if low&arg != 0 {
		return "", fmt.Errorf("SID %s already has bits of argument %d set", sid, arg)
	}
	binary.BigEndian.PutUint64(b[8:], low|arg)
	return netip.AddrFrom16(b).String(), nil
}

// Color is the SR Policy color of a stream to the OUT: one per OUT, so policies of one headend to
// different OUTs never collide (RFC 9256: a headend tells policies apart by color and endpoint).
func (p Renderer) Color(destinationID string) uint32 {
	base := p.ColorBase
	if base == 0 {
		base = 100
	}
	i := slices.IndexFunc(p.Resources.TED.Destinations, func(e domain.Endpoint) bool { return e.ID == destinationID })
	return base + uint32(i+1)
}

// Policy is the SR Policy of a leg of a stream to the OUT.
func (p Renderer) Policy(l Leg, destinationID string) domain.SRPolicy {
	src, _ := p.Resources.Source(l.Source)
	return domain.SRPolicy{
		Name:     fmt.Sprintf("visfox-%s-%s-%s", l.Source, destinationID, l.Endpoint),
		Headend:  src.NodeID,
		Endpoint: l.Endpoint,
		Color:    p.Color(destinationID),
		// Segments carry the arguments already.
		SegmentList: slices.Clone(l.Segments),
	}
}

// Configs returns the function.set_params instructions for the request's SF instances whose node
// does not run the instance's config yet (configured: node id -> SFInstance.Config).
func (p Renderer) Configs(s domain.SFCRequest, al domain.SFP, configured map[string]string) []domain.Instruction {
	var out []domain.Instruction
	for i, b := range s.SFs {
		if i >= len(al.Nodes) || al.Nodes[i] == "" || configured[al.Nodes[i]] == b.Config() {
			continue
		}
		fns := make([]domain.FunctionConfig, len(b.Functions))
		for j, fn := range b.Functions {
			fns[j] = domain.FunctionConfig{InstanceID: fn.InstanceId, Type: string(fn.Type), Params: fn.Params}
		}
		out = append(out, domain.Instruction{Op: domain.OpFunctionSetParams, Target: al.Nodes[i], Functions: fns})
	}
	return out
}

// Realize renders bringing the OUT's streams to the state of the request:
// req is the OUT's request (nil when no intent is applied); its on-air IN runs the whole path, and
// every other IN of sfp.Feeds (hot standby) is sent into the OUT's switchers by a feed policy that
// ends after the switchers (it carries no VVF). configured are the configs the VVF nodes run and
// installed the SR Policies the headends hold.
//
// With take the on-air stream is switched to now (a Take, or a restore): its VVF configs come first,
// so the picture never shows a half-configured chain, then its policy (a feed policy turns into the
// whole path before the switch), the switch, and at a swap switcher the main stream's policy right
// after the switch. Without take the on-air stream is left as it is unless its policies differ.
//
// Plan.Instructions are what must run before the OUT's state changes (new feeds, the switch);
// Plan.Release what runs after it: feeds that changed (a stream that stepped down) and the OUT's
// installed policies nothing uses any more, except those of main streams of swap switchers, which
// keep sending.
func (p Renderer) Realize(destinationID string, req *domain.SFCRequest, sfp domain.SFP, take bool,
	configured map[string]string, installed []domain.SRPolicy) (domain.Plan, error) {
	color := p.Color(destinationID)
	have := map[string]domain.SRPolicy{}
	for _, pol := range installed {
		if pol.Color == color {
			have[PolicyKey(pol)] = pol
		}
	}
	differs := func(pol domain.SRPolicy) bool {
		h, ok := have[PolicyKey(pol)]
		return !ok || !slices.Equal(h.SegmentList, pol.SegmentList)
	}
	set := func(pol domain.SRPolicy, note string) domain.Instruction {
		return domain.Instruction{Op: domain.OpSRPolicySet, Target: pol.Headend, Policy: &pol, Note: note}
	}
	var plan domain.Plan
	keep := map[string]bool{}

	onAir := ""
	var carriers []domain.Instruction
	if req != nil {
		plan.CellID = req.IntentID
	}
	if req != nil && req.OnAir {
		onAir = req.SourceID
		legs, err := p.Legs(*req, sfp)
		if err != nil {
			return domain.Plan{}, err
		}
		if take {
			plan.Instructions = append(plan.Instructions, p.Configs(*req, sfp, configured)...)
		}
		for _, l := range legs {
			pol := p.Policy(l, destinationID)
			keep[PolicyKey(pol)] = true
			if !take && !differs(pol) {
				continue
			}
			if l.Carrier {
				carriers = append(carriers, set(pol, "main stream carries the path after the swap switcher"))
				continue
			}
			in := set(pol, "")
			if _, ok := have[PolicyKey(pol)]; take && !ok && len(sfp.Way) > 0 {
				// Not fed before (cold standby): the stream must reach the switchers before they
				// switch to it.
				in.NewStream = true
				in.Note = "new stream"
			}
			plan.Instructions = append(plan.Instructions, in)
		}
	}

	// Feeds: new ones go first (they are not on air), changed ones after the switch.
	var changed []domain.Instruction
	decapOf := ""
	if dst, ok := p.Resources.Destination(destinationID); ok {
		decapOf = dst.NodeID
	}
	for _, src := range slices.Sorted(maps.Keys(sfp.Feeds)) {
		if src == onAir {
			continue
		}
		legs, err := p.Legs(domain.SFCRequest{SourceID: src, DestinationID: destinationID}, domain.SFP{Way: sfp.Feeds[src]})
		if err != nil {
			return domain.Plan{}, err
		}
		for _, l := range legs {
			if l.Carrier {
				continue // the main stream of a swap switcher belongs to the on-air stream
			}
			if l.Endpoint == decapOf {
				// A feed ends at the input of the OUT's root switcher: when that switcher still selects
				// it with nothing on air, it goes no further. The policy keeps its endpoint, so a Take
				// updates the same policy (same headend flow) to the whole path.
				l.Segments = l.Segments[:len(l.Segments)-1]
			}
			pol := p.Policy(l, destinationID)
			if keep[PolicyKey(pol)] {
				continue
			}
			keep[PolicyKey(pol)] = true
			if _, ok := have[PolicyKey(pol)]; !ok {
				plan.Instructions = append(plan.Instructions, set(pol, "standby feed"))
			} else if differs(pol) {
				changed = append(changed, set(pol, "standby feed"))
			}
		}
	}

	if take && req != nil && req.OnAir {
		for _, h := range sfp.Way {
			in, err := p.Input(h)
			if err != nil {
				return domain.Plan{}, err
			}
			plan.Instructions = append(plan.Instructions, domain.Instruction{
				Op: domain.OpSwitcherSelect, Target: h.Node, Select: &domain.SwitcherSelect{Port: in.Port, Next: in.Next},
			})
		}
	}
	plan.Instructions = append(plan.Instructions, carriers...)

	plan.Release = changed
	for _, key := range slices.Sorted(maps.Keys(have)) {
		pol := have[key]
		if keep[key] {
			continue
		}
		if p.isMainHeadend(pol.Headend) {
			// The main stream of a swap switcher keeps sending; with nothing on air it ends at the
			// switcher, like a feed.
			if onAir == "" && len(pol.SegmentList) > 1 {
				pol.SegmentList = pol.SegmentList[:1]
				plan.Release = append(plan.Release, set(pol, "main stream ends at the swap switcher"))
			}
			continue
		}
		plan.Release = append(plan.Release, domain.Instruction{Op: domain.OpSRPolicyDelete, Target: pol.Headend, Policy: &pol})
	}
	return plan, nil
}

// isMain reports whether the IN feeds the main input of a swap switcher.
func (p Renderer) isMain(sourceID string) bool {
	for _, f := range p.Resources.FlowNodes {
		id := f.ID
		if f.Mode != SwitcherSwap {
			continue
		}
		if main, _, err := p.mainSource(id); err == nil && main == sourceID {
			return true
		}
	}
	return false
}

// SegmentList is the effective Segment List of a request for diagnostics: its own legs, with the
// path a swap switcher's main stream carries after the switcher.
func SegmentList(legs []Leg) []string {
	var out []string
	for _, l := range legs {
		if l.Carrier && len(l.Segments) > 0 {
			out = append(out, l.Segments[1:]...)
			continue
		}
		out = append(out, l.Segments...)
	}
	return out
}

// PolicyKey is how a headend tells SR Policies apart: headend, color and endpoint (RFC 9256).
func PolicyKey(p domain.SRPolicy) string {
	return fmt.Sprintf("%s|%d|%s", p.Headend, p.Color, p.Endpoint)
}

// isMainHeadend reports whether the headend sends a main stream of a swap switcher.
func (p Renderer) isMainHeadend(nodeID string) bool {
	for _, src := range p.Resources.TED.Sources {
		if src.NodeID == nodeID && p.isMain(src.ID) {
			return true
		}
	}
	return false
}
