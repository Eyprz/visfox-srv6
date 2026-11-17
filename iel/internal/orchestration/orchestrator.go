// Package orchestration is Intent Orchestration (RFC 9315 §5.1.3): it takes the SFC requests
// Intent Translation made from the SSoT and realizes them on the network in three steps, each with
// its own package and data:
//
//	plan      SFCRequest -> SFP          (SFC Validator for resources, path selection)
//	render    SFP -> domain.Plan          (SR Policies, VVF instructions: switcher selections and configs)
//	provision domain.Plan -> SBI clients  (configure / provision: live or dry run)
//
// The Orchestrator keeps what it derived from the intents and provisioned (the selected SFPs, the
// config every VVF node runs and the SR Policies on the headends). The caller stores it in the SSoT
// after every change (Realization) and loads it at startup (Load), so a restart keeps the paths on
// air. It is not safe for concurrent use; the caller serializes.
package orchestration

import (
	"context"
	"maps"
	"slices"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/orchestration/plan"
	"github.com/eyprz/visfox-srv6/iel/internal/orchestration/render"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
)

// Provisioner runs rendered instructions southbound in order and stops at the first failure
// (internal/orchestration/provision).
type Provisioner interface {
	Provision(ctx context.Context, ins []domain.Instruction) error
}

// Options configure an Orchestrator.
type Options struct {
	Resources *ssot.Resources
	// SwitcherRequired: every stream passes a select VVF (a switcher; domain policy).
	SwitcherRequired bool
	// Arg is the ARG rule of the SID interpretation rule ("egress", "stream" or "none").
	Arg string
	// ColorBase is the base of the SR Policy colors (one color per OUT).
	ColorBase uint32
	// Provisioner runs the instructions (required).
	Provisioner Provisioner
}

// Orchestrator plans, renders and provisions SFC requests.
type Orchestrator struct {
	selector    plan.Selector
	renderer    render.Renderer
	provisioner Provisioner
	// sfps is the SFP every request (destination id) uses now; configured is the config every VVF
	// node runs now (node id -> SFInstance.Config); policies are the SR Policies the headends hold
	// (render.PolicyKey -> policy). The caller stores them in the SSoT (Realization).
	sfps       map[string]domain.SFP
	configured map[string]string
	policies   map[string]domain.SRPolicy
}

// New returns an Orchestrator with nothing selected or configured.
func New(o Options) *Orchestrator {
	return &Orchestrator{
		selector:    plan.Selector{Resources: o.Resources, SwitcherRequired: o.SwitcherRequired},
		renderer:    render.Renderer{Resources: o.Resources, Arg: o.Arg, ColorBase: o.ColorBase},
		provisioner: o.Provisioner,
		sfps:        map[string]domain.SFP{}, configured: map[string]string{}, policies: map[string]domain.SRPolicy{},
	}
}

// SetResources switches planning and rendering to new resources (the TED changed). What was
// selected and provisioned stays; the next Refresh plans on the new resources.
func (o *Orchestrator) SetResources(r *ssot.Resources) {
	o.selector.Resources, o.renderer.Resources = r, r
}

// Load replaces what the orchestrator knows with a realization read from the SSoT.
func (o *Orchestrator) Load(r ssot.Realization) {
	o.sfps, o.configured, o.policies = map[string]domain.SFP{}, map[string]string{}, map[string]domain.SRPolicy{}
	maps.Copy(o.sfps, r.SFPs)
	maps.Copy(o.configured, r.VVFConfigs)
	for _, p := range r.Policies {
		o.policies[render.PolicyKey(p)] = p
	}
}

// Realization is what the orchestrator selected and provisioned, to store in the SSoT.
func (o *Orchestrator) Realization() ssot.Realization {
	r := ssot.Realization{SFPs: maps.Clone(o.sfps), VVFConfigs: maps.Clone(o.configured)}
	r.Policies = o.Policies()
	return r
}

// Observe replaces the SR Policies the orchestrator believes the headends hold with what monitoring
// observed, so the next restore renders against the network as it is.
func (o *Orchestrator) Observe(policies []domain.SRPolicy) {
	o.policies = map[string]domain.SRPolicy{}
	for _, p := range policies {
		o.policies[render.PolicyKey(p)] = p
	}
}

// Color is the SR Policy color of the OUT.
func (o *Orchestrator) Color(destinationID string) uint32 { return o.renderer.Color(destinationID) }

// Policies are the SR Policies the headends hold, by headend, color and endpoint.
func (o *Orchestrator) Policies() []domain.SRPolicy {
	keys := slices.Sorted(maps.Keys(o.policies))
	out := make([]domain.SRPolicy, len(keys))
	for i, k := range keys {
		out[i] = o.policies[k]
	}
	return out
}

// ---------------------------------------------------------------- plan

// Select selects SFPs for the requests from the current state without adopting them. fresh names
// the requests being taken (destination ids): make-before-break.
func (o *Orchestrator) Select(reqs []domain.SFCRequest, fresh map[string]bool) plan.Selection {
	return o.selector.Select(reqs, plan.State{Previous: o.sfps, Configured: o.configured}, fresh)
}

// Feasibility is why the selection does not fit the resource pools (nil: it fits).
func (o *Orchestrator) Feasibility(sel plan.Selection) *apitypes.ValidationIssue {
	return o.selector.Feasibility(sel)
}

// Issues are every shortage of the selection (empty: it fits), those of focus (the OUT a change is
// for) first.
func (o *Orchestrator) Issues(sel plan.Selection, focus string) []apitypes.ValidationIssue {
	return o.selector.Issues(sel, focus)
}

// Adopt makes the selection the current SFPs.
func (o *Orchestrator) Adopt(sel plan.Selection) { o.sfps = sel.SFPs }

// Refresh selects and adopts SFPs for the requests after a change. Nodes no on-air request uses
// keep no live config.
func (o *Orchestrator) Refresh(reqs []domain.SFCRequest) {
	sel := o.Select(reqs, nil)
	o.Adopt(sel)
	live := map[string]bool{}
	for _, r := range reqs {
		if r.OnAir {
			for _, id := range sel.SFPs[r.DestinationID].Nodes {
				live[id] = true
			}
		}
	}
	for id := range o.configured {
		if !live[id] {
			delete(o.configured, id)
		}
	}
}

// SFP is the path the OUT's request uses now.
func (o *Orchestrator) SFP(destinationID string) domain.SFP { return o.sfps[destinationID] }

// ---------------------------------------------------------------- render

// RenderRealize renders bringing the OUT's streams to the request's state on the SFP (render.Realize):
// req is the OUT's request (nil when no intent is applied), take switches its on-air IN to the OUT
// now. Plan.Instructions run before the OUT's state changes, Plan.Release after it.
func (o *Orchestrator) RenderRealize(destinationID string, req *domain.SFCRequest, sfp domain.SFP, take bool) (domain.Plan, error) {
	return o.renderer.Realize(destinationID, req, sfp, take, o.configured, o.Policies())
}

// RenderRestore renders bringing the OUT back to the SSoT after a failed change, or after the network
// drifted from it: req is the OUT's request in the SSoT (nil: none) on the OUT's current SFP. The on-air
// stream is switched to again, the feeds are set and the OUT's other policies are withdrawn.
func (o *Orchestrator) RenderRestore(destinationID string, req *domain.SFCRequest) ([]domain.Instruction, error) {
	pl, err := o.renderer.Realize(destinationID, req, o.sfps[destinationID], true, o.configured, o.Policies())
	if err != nil {
		return nil, err
	}
	out := pl.Instructions
	for _, in := range pl.Release {
		if in.Op == domain.OpSRPolicyDelete {
			in.Note = "not part of the known state"
		}
		out = append(out, in)
	}
	return out, nil
}

// Desired are the SR Policies the requests need on their current SFPs: what the headends should hold
// according to the SSoT (Intent Assurance compares it with the network).
func (o *Orchestrator) Desired(reqs []domain.SFCRequest) ([]domain.SRPolicy, error) {
	var out []domain.SRPolicy
	for i := range reqs {
		pl, err := o.renderer.Realize(reqs[i].DestinationID, &reqs[i], o.sfps[reqs[i].DestinationID], false, o.configured, nil)
		if err != nil {
			return nil, err
		}
		for _, in := range append(pl.Instructions, pl.Release...) {
			if in.Op == domain.OpSRPolicySet {
				out = append(out, *in.Policy)
			}
		}
	}
	return out, nil
}

// Selections are the inputs the switcher VVFs should send on according to the SSoT: along the way of
// every on-air request. A switcher on no on-air way has no expected input.
func (o *Orchestrator) Selections(reqs []domain.SFCRequest) ([]domain.Selection, error) {
	var out []domain.Selection
	for _, r := range reqs {
		if !r.OnAir {
			continue
		}
		for _, h := range o.sfps[r.DestinationID].Way {
			in, err := o.renderer.Input(h)
			if err != nil {
				return nil, err
			}
			out = append(out, domain.Selection{DestinationID: r.DestinationID, Node: h.Node, Next: in.Next})
		}
	}
	return out, nil
}

// SegmentList is the effective Segment List of the request on the SFP (diagnostics).
func (o *Orchestrator) SegmentList(req domain.SFCRequest, sfp domain.SFP) ([]string, error) {
	legs, err := o.renderer.Legs(req, sfp)
	if err != nil {
		return nil, err
	}
	return render.SegmentList(legs), nil
}

// InputSID is the SID of the input port a hop enters on (diagnostics).
func (o *Orchestrator) InputSID(h domain.Hop) (string, error) {
	in, err := o.renderer.Input(h)
	return in.SID, err
}

// ---------------------------------------------------------------- provision

// Provision runs the instructions southbound.
func (o *Orchestrator) Provision(ctx context.Context, ins []domain.Instruction) error {
	if len(ins) == 0 {
		return nil
	}
	return o.provisioner.Provision(ctx, ins)
}

// Remember records what provisioned instructions changed: the configs of VVF nodes and the SR
// Policies of the headends.
func (o *Orchestrator) Remember(ins []domain.Instruction) {
	for _, in := range ins {
		switch in.Op {
		case domain.OpFunctionSetParams:
			o.configured[in.Target] = domain.ConfigOf(in.Functions)
		case domain.OpSRPolicySet:
			o.policies[render.PolicyKey(*in.Policy)] = *in.Policy
		case domain.OpSRPolicyDelete:
			delete(o.policies, render.PolicyKey(*in.Policy))
		}
	}
}
