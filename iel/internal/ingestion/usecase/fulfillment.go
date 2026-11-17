package usecase

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/orchestration/plan"
	"github.com/eyprz/visfox-srv6/iel/internal/orchestration/provision"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
)

// This file hands the intents to the fulfillment pipeline: Active Intent -> domain.Intent ->
// (translation) domain.SFCRequest -> (orchestration) SFP, Plan, provisioning.

// change is a hypothetical change to the Active Intent Table (an intent's state and / or chain), to
// check a step before taking it.
type change struct {
	state func(c *ssot.ActiveIntent) apitypes.ExecutionState
	chain func(c *ssot.ActiveIntent) []apitypes.FunctionInstance
}

func (s *env) chainOf(c *ssot.ActiveIntent) []apitypes.FunctionInstance {
	if c.PresetID == nil {
		return nil
	}
	if p := s.preset(*c.PresetID); p != nil {
		return p.Chain
	}
	return nil
}

// intentOf is the crosspoint intent of a stored Active Intent and its preset.
func (s *env) intentOf(c *ssot.ActiveIntent, chain []apitypes.FunctionInstance, onAir bool) domain.Intent {
	return domain.Intent{ID: c.ID, SourceID: c.SourceID, DestinationID: c.DestinationID, Chain: chain, OnAir: onAir}
}

// requests are the SFC requests of the OUTs with an applied intent after the change: the on-air
// intent's, else the first standby intent's (VVF nodes are reserved at STANDBY), with the INs of the
// OUT's standby intents (hot standby feeds them into the OUT's switchers).
func (s *env) requests(ch change) []domain.SFCRequest {
	stateOf := func(c *ssot.ActiveIntent) apitypes.ExecutionState {
		if ch.state != nil {
			return ch.state(c)
		}
		return c.State
	}
	byOut := map[string][]*ssot.ActiveIntent{}
	var order []string
	for _, id := range s.cellOrder {
		c := s.cells[id]
		if st := stateOf(c); st != apitypes.ExecutionStateActive && st != apitypes.ExecutionStateStandby {
			continue
		}
		if _, ok := byOut[c.DestinationID]; !ok {
			order = append(order, c.DestinationID)
		}
		byOut[c.DestinationID] = append(byOut[c.DestinationID], c)
	}
	out := make([]domain.SFCRequest, 0, len(order))
	for _, d := range order {
		cells := byOut[d]
		pick := cells[0]
		for _, c := range cells {
			if stateOf(c) == apitypes.ExecutionStateActive {
				pick = c
				break
			}
		}
		chain := s.chainOf(pick)
		if ch.chain != nil {
			chain = ch.chain(pick)
		}
		r := s.tr.Translate(s.intentOf(pick, chain, stateOf(pick) == apitypes.ExecutionStateActive))
		for _, c := range cells {
			if stateOf(c) == apitypes.ExecutionStateStandby {
				r.Standby = append(r.Standby, c.SourceID)
			}
		}
		out = append(out, r)
	}
	return out
}

// requestOf returns the request of the OUT.
func requestOf(reqs []domain.SFCRequest, destinationID string) (domain.SFCRequest, bool) {
	for _, r := range reqs {
		if r.DestinationID == destinationID {
			return r, true
		}
	}
	return domain.SFCRequest{}, false
}

// requestPtr returns the request of the OUT, nil when the OUT has no applied intent.
func requestPtr(reqs []domain.SFCRequest, destinationID string) *domain.SFCRequest {
	if r, ok := requestOf(reqs, destinationID); ok {
		return &r
	}
	return nil
}

// selectAfter selects the paths of the requests after a change.
func (s *env) selectAfter(ch change, fresh map[string]bool) (plan.Selection, []domain.SFCRequest) {
	reqs := s.requests(ch)
	return s.orch.Select(reqs, fresh), reqs
}

// refresh re-plans the requests after the SSoT changed and stores the realization.
func (s *env) refresh(ctx context.Context) {
	s.orch.Refresh(s.requests(change{}))
	s.persist(ctx)
}

// persist stores what orchestration selected and provisioned in the SSoT: derived state is not
// kept only in memory.
func (s *env) persist(ctx context.Context) {
	if err := s.store.PutRealization(ctx, s.orch.Realization(), s.now()); err != nil {
		s.log(apitypes.LogLevelError, "storing the realization failed: "+err.Error(), "", string(apitypes.ErrorCodeINTERNALERROR))
	}
}

// takeChange is the change a Take makes: the intent goes on air, the OUT's on-air intent steps down.
func takeChange(cell *ssot.ActiveIntent) change {
	return change{state: func(c *ssot.ActiveIntent) apitypes.ExecutionState {
		switch {
		case c.ID == cell.ID:
			return apitypes.ExecutionStateActive
		case c.DestinationID == cell.DestinationID && c.State == apitypes.ExecutionStateActive:
			return apitypes.ExecutionStateStandby
		}
		return c.State
	}}
}

// then is the change ch followed by setting the cells' states (the others keep ch's state).
func (ch change) then(states map[string]apitypes.ExecutionState) change {
	prev := ch.state
	return change{chain: ch.chain, state: func(c *ssot.ActiveIntent) apitypes.ExecutionState {
		if st, ok := states[c.ID]; ok {
			return st
		}
		if prev != nil {
			return prev(c)
		}
		return c.State
	}}
}

// capacityIssues are why the requests would not fit the pools after the change (empty: they fit). Only
// shortages of the OUTs the change touches, or of OUTs that fit before it, count: OUTs whose intents
// already do not fit (e.g. after the operator config got stricter) do not block other changes, and
// their own STANDBY cells show why they cannot be taken.
// touched[0] is the OUT the change is for: its shortages come first. fresh names the OUTs being
// taken (their on-air IN may move; the others keep theirs first).
func (s *env) capacityIssues(ch change, fresh map[string]bool, touched ...string) []apitypes.ValidationIssue {
	sel, _ := s.selectAfter(ch, fresh)
	focus := ""
	if len(touched) > 0 {
		focus = touched[0]
	}
	return s.orch.Issues(s.newShortages(sel, touched), focus)
}

// newShortages keeps the failures of the selection on the touched OUTs and on the OUTs the current
// SSoT fits.
func (s *env) newShortages(sel plan.Selection, touched []string) plan.Selection {
	if len(sel.Failed) == 0 {
		return sel
	}
	base, _ := s.selectAfter(change{}, nil)
	broken := map[string]bool{}
	for _, f := range base.Failed {
		broken[f.DestinationID] = true
	}
	var kept []plan.Failure
	for _, f := range sel.Failed {
		if slices.Contains(touched, f.DestinationID) || !broken[f.DestinationID] {
			kept = append(kept, f)
		}
	}
	sel.Failed = kept
	return sel
}

// run provisions instructions southbound and turns a failure into an API error (ROUTER_ERROR /
// ROUTER_TIMEOUT). Every instruction is logged (so dry runs show what would be sent). What the
// instructions changed, also those before a failure, is recorded and stored in the SSoT.
func (s *env) run(ctx context.Context, cellID string, ins []domain.Instruction) error {
	if len(ins) == 0 {
		return nil
	}
	for _, in := range ins {
		s.log(apitypes.LogLevelInfo, "southbound: "+in.Describe(), cellID, "")
	}
	s.gen++
	err := s.orch.Provision(ctx, ins)
	if err == nil {
		s.orch.Remember(ins)
		s.persist(ctx)
		return nil
	}
	code := apitypes.ErrorCodeROUTERERROR
	var pe *provision.Error
	if errors.As(err, &pe) {
		s.orch.Remember(pe.Applied)
		s.persist(ctx)
		if pe.Timeout {
			code = apitypes.ErrorCodeROUTERTIMEOUT
		}
	}
	s.log(apitypes.LogLevelError, fmt.Sprintf("%s: southbound instruction failed: %v", cellID, err), cellID, string(code))
	return &domain.Error{Code: code, Message: err.Error(), Details: map[string]any{}}
}

// planError is a request that orchestration cannot express southbound (network operator config).
func planError(err error) *domain.Error {
	return &domain.Error{Code: apitypes.ErrorCodeINTERNALERROR, Message: "Cannot plan the stream: " + err.Error()}
}

// realize provisions the streams of the OUTs after a change that switches nothing (sel and reqs are
// the selection and requests after it), in order: the policies nothing uses any more are withdrawn
// first (a headend or switcher input being released may be the one the next feed needs), then the
// new and changed feeds are set. In cold standby a change of STANDBY intents sends nothing.
func (s *env) realize(ctx context.Context, cellID string, destinations []string, sel plan.Selection, reqs []domain.SFCRequest) error {
	// OUTs whose ways the selection moved to make room (STANDBY feeds packed again) go first: they
	// free what the OUTs of the change take.
	var moved []string
	for _, d := range slices.Sorted(maps.Keys(sel.SFPs)) {
		if !slices.Contains(destinations, d) && !reflect.DeepEqual(sel.SFPs[d], s.orch.SFP(d)) {
			moved = append(moved, d)
		}
	}
	seen := map[string]bool{}
	for _, d := range slices.Concat(moved, destinations) {
		if seen[d] {
			continue
		}
		seen[d] = true
		pl, err := s.orch.RenderRealize(d, requestPtr(reqs, d), sel.SFPs[d], false)
		if err != nil {
			return planError(err)
		}
		var withdraw, set []domain.Instruction
		for _, in := range pl.Release {
			if in.Op == domain.OpSRPolicyDelete {
				withdraw = append(withdraw, in)
			} else {
				set = append(set, in)
			}
		}
		if err := s.run(ctx, cellID, slices.Concat(withdraw, pl.Instructions, set)); err != nil {
			return err
		}
	}
	return nil
}

// reapply sends an on-air intent's VVF configs and path again for a new chain (another preset, or
// new params): the stream keeps its switchers and gets the nodes the new chain fits.
func (s *env) reapply(ctx context.Context, c *ssot.ActiveIntent, chain []apitypes.FunctionInstance) error {
	ch := change{chain: func(o *ssot.ActiveIntent) []apitypes.FunctionInstance {
		if o.ID == c.ID {
			return chain
		}
		return s.chainOf(o)
	}}
	sel, reqs := s.selectAfter(ch, nil)
	if issue := s.orch.Feasibility(s.newShortages(sel, []string{c.DestinationID})); issue != nil {
		return &domain.Error{Code: apitypes.ErrorCodeRESOURCEEXHAUSTED, Message: issue.Message,
			Details: map[string]any{"issues": []apitypes.ValidationIssue{*issue}}}
	}
	pl, err := s.orch.RenderRealize(c.DestinationID, requestPtr(reqs, c.DestinationID), sel.SFPs[c.DestinationID], true)
	if err != nil {
		return planError(err)
	}
	// The switchers already send this stream: only the configs and the paths change.
	var ins []domain.Instruction
	for _, in := range append(pl.Instructions, pl.Release...) {
		if in.Op != domain.OpSwitcherSelect {
			ins = append(ins, in)
		}
	}
	if err := s.run(ctx, c.ID, ins); err != nil {
		return err
	}
	s.orch.Adopt(sel)
	return nil
}
