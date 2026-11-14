package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
)

func cellIDs(cells []*ssot.ActiveIntent) []string {
	ids := make([]string, len(cells))
	for i, c := range cells {
		ids[i] = c.ID
	}
	return ids
}

func cellIDsOrEmpty(cells []*ssot.ActiveIntent) []string {
	if len(cells) == 0 {
		return []string{}
	}
	return cellIDs(cells)
}

// GetMatrixState returns every cell.
// It is getMatrixState in api/openapi.yaml.
type GetMatrixState struct{ env *env }

// Run runs the use case.
func (u GetMatrixState) Run() apitypes.MatrixState {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	cells := make([]apitypes.MatrixCell, 0, len(s.cellOrder))
	for _, id := range s.cellOrder {
		cells = append(cells, s.view(s.cells[id]))
	}
	now := s.now()
	return apitypes.MatrixState{Cells: cells, UpdatedAt: &now}
}

// GetMatrixCell returns one cell.
// It is getMatrixCell in api/openapi.yaml.
type GetMatrixCell struct{ env *env }

// Run runs the use case.
func (u GetMatrixCell) Run(id string) (apitypes.MatrixCell, error) {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cells[id]
	if !ok {
		return apitypes.MatrixCell{}, domain.NotFound("Cell")
	}
	return s.view(c), nil
}

// takeIssues lists why a standby cell cannot be taken now: the IN unicast, switcher and VVF node
// pools after the Take (with hot standby the IN and switchers are reserved at STANDBY already).
func (s *env) takeIssues(c *ssot.ActiveIntent) []apitypes.ValidationIssue {
	issues := []apitypes.ValidationIssue{}
	if c.State != apitypes.ExecutionStateStandby {
		return issues
	}
	return append(issues, s.capacityIssues(takeChange(c), map[string]bool{c.DestinationID: true}, c.DestinationID)...)
}

// assignChange is the change of assigning a chain to a cell: the cells in release turn OFF and an
// OFF cell becomes STANDBY.
func (s *env) assignChange(c *ssot.ActiveIntent, chain []apitypes.FunctionInstance, release []string) change {
	states := map[string]apitypes.ExecutionState{}
	for _, id := range release {
		states[id] = apitypes.ExecutionStateOff
	}
	if c.State == apitypes.ExecutionStateOff {
		states[c.ID] = apitypes.ExecutionStateStandby
	}
	return change{chain: func(o *ssot.ActiveIntent) []apitypes.FunctionInstance {
		if o.ID == c.ID {
			return chain
		}
		return s.chainOf(o)
	}}.then(states)
}

// maxReleaseCandidates bounds the STANDBY cells searched for a release (2^n selections).
const maxReleaseCandidates = 10

// releasable is the smallest set of STANDBY cells whose release makes the assignment fit (nil: none
// does). Only the cell's IN on other OUTs is offered: the release lets the user move an IN that is
// already sent elsewhere (its streams ran short), never turns off another IN's STANDBY for an
// unrelated shortage. ON AIR cells are never released.
func (s *env) releasable(c *ssot.ActiveIntent, chain []apitypes.FunctionInstance, release []string) []string {
	var cands []string
	for _, id := range s.cellOrder {
		o := s.cells[id]
		if o.ID != c.ID && o.SourceID == c.SourceID && o.State == apitypes.ExecutionStateStandby &&
			!slices.Contains(release, o.ID) {
			cands = append(cands, o.ID)
		}
	}
	if len(cands) > maxReleaseCandidates {
		cands = cands[:maxReleaseCandidates]
	}
	for size := 1; size <= len(cands); size++ {
		var found []string
		combinations(len(cands), size, func(idx []int) bool {
			ids := slices.Clone(release)
			for _, i := range idx {
				ids = append(ids, cands[i])
			}
			if len(s.capacityIssues(s.assignChange(c, chain, ids), nil, c.DestinationID)) == 0 {
				found = ids[len(release):]
				return true
			}
			return false
		})
		if found != nil {
			return found
		}
	}
	return nil
}

// combinations calls f with every k-subset of 0..n-1 (in lexicographic order) until f returns true.
func combinations(n, k int, f func([]int) bool) bool {
	idx := make([]int, k)
	var rec func(start, depth int) bool
	rec = func(start, depth int) bool {
		if depth == k {
			return f(idx)
		}
		for i := start; i <= n-(k-depth); i++ {
			idx[depth] = i
			if rec(i+1, depth+1) {
				return true
			}
		}
		return false
	}
	return rec(0, 0)
}

// checkRelease checks the cells an assignment releases: they exist, are not the cell itself, and
// are not on air.
func (s *env) checkRelease(c *ssot.ActiveIntent, release []string) error {
	for _, id := range release {
		o, ok := s.cells[id]
		switch {
		case !ok:
			return domain.NotFound("Cell " + id)
		case id == c.ID:
			return domain.Fail(apitypes.ErrorCodeINVALIDREQUEST, "%s cannot release itself", c.ID)
		case o.State == apitypes.ExecutionStateActive:
			return domain.Fail(apitypes.ErrorCodeINVALIDCELLSTATE, "%s is on air and cannot be released", id)
		}
	}
	return nil
}

// validateAssignment checks a preset's chain for a cell: the IN / OUT are reachable, the chain
// turns the IN's format into the OUT's, and every stream still fits the pools with the cell applied
// and the cells in release turned OFF (a new stream needs free VVF nodes, with hot standby its IN
// and switchers too; a change on air may need others). A shortage names the smallest set of
// STANDBY cells whose release would make it fit (releasable_cells).
func (s *env) validateAssignment(c *ssot.ActiveIntent, chain []apitypes.FunctionInstance, release []string) apitypes.MatrixValidationResult {
	errs := []apitypes.ValidationIssue{}
	src, srcOK := s.res.Source(c.SourceID)
	dst, dstOK := s.res.Destination(c.DestinationID)
	if c.State == apitypes.ExecutionStateDisconnected || !srcOK || !dstOK {
		errs = append(errs, apitypes.ValidationIssue{Code: "NODE_OFFLINE", Message: "Source or destination is disconnected"})
	} else if issue := s.tr.CheckFormat(apitypes.VideoFormat(src.VideoFormat), apitypes.VideoFormat(dst.VideoFormat), chain); issue != nil {
		errs = append(errs, apitypes.ValidationIssue{Code: string(apitypes.ChainIssueCodeFORMATMISMATCH), Message: issue.Message})
	}
	if c.State != apitypes.ExecutionStateDisconnected {
		if issues := s.capacityIssues(s.assignChange(c, chain, release), nil, c.DestinationID); len(issues) > 0 {
			// Every shortage is listed; a release is offered only when it solves all of them.
			if ids := s.releasable(c, chain, release); len(ids) > 0 {
				for i := range issues {
					issues[i].ReleasableCells = &ids
				}
			}
			errs = append(errs, issues...)
		}
	}
	r := apitypes.MatrixValidationResult{Feasible: len(errs) == 0, Errors: &errs}
	if len(errs) > 0 {
		r.Reason = &errs[0].Message
	}
	return r
}

// ValidateMatrixAssignment checks whether a preset can be assigned to a cell (POST /matrix/validate).
// It is validateMatrixAssignment in api/openapi.yaml.
type ValidateMatrixAssignment struct{ env *env }

// Run runs the use case.
func (u ValidateMatrixAssignment) Run(cellID, presetID string, release []string) (apitypes.MatrixValidationResult, error) {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cells[cellID]
	if !ok {
		return apitypes.MatrixValidationResult{}, domain.NotFound("Cell")
	}
	p := s.preset(presetID)
	if p == nil {
		return apitypes.MatrixValidationResult{}, domain.NotFound("Preset")
	}
	if err := s.checkRelease(c, release); err != nil {
		return apitypes.MatrixValidationResult{}, err
	}
	return s.validateAssignment(c, p.Chain, release), nil
}

// ApplyPresetToCell applies a preset to a cell. An OFF cell becomes STANDBY; an applied cell keeps its
// state and runs the new preset. The STANDBY cells in release turn OFF in the same change (the user
// accepted releasing them). With hot standby a new STANDBY IN is sent into the OUT's switchers.
// It is applyPresetToCell in api/openapi.yaml.
type ApplyPresetToCell struct{ env *env }

// Run runs the use case.
func (u ApplyPresetToCell) Run(ctx context.Context, cellID, presetID string, release []string) (apitypes.MatrixCell, error) {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cells[cellID]
	if !ok {
		return apitypes.MatrixCell{}, domain.NotFound("Cell")
	}
	p := s.preset(presetID)
	if p == nil {
		return apitypes.MatrixCell{}, domain.NotFound("Preset")
	}
	if c.State == apitypes.ExecutionStateDisconnected {
		return apitypes.MatrixCell{}, domain.Fail(apitypes.ErrorCodeINVALIDCELLSTATE, "%s is disconnected", c.ID)
	}
	if err := s.checkRelease(c, release); err != nil {
		return apitypes.MatrixCell{}, err
	}
	if v := s.validateAssignment(c, p.Chain, release); !v.Feasible {
		reason := "Infeasible"
		if v.Reason != nil {
			reason = *v.Reason
		}
		return apitypes.MatrixCell{}, &domain.Error{Code: apitypes.ErrorCodeINFEASIBLEASSIGNMENT, Message: reason,
			Details: toMap(v)}
	}
	var released []*ssot.ActiveIntent
	for _, id := range release {
		if o := s.cells[id]; o.State == apitypes.ExecutionStateStandby {
			released = append(released, o)
		}
	}
	affected := append([]*ssot.ActiveIntent{c}, released...)
	attempt := map[string]apitypes.CellConfigSnapshot{c.ID: s.snapshot(p.Id, p.Chain)}
	describe := fmt.Sprintf("preset '%s'", p.Name)
	if c.State == apitypes.ExecutionStateActive {
		// On air the new preset's VVF configs and path go southbound before the state changes.
		if err := s.reapply(ctx, c, p.Chain); err != nil {
			return apitypes.MatrixCell{}, s.rollBack(ctx, affected, attempt, describe, err)
		}
	}
	// Policies of released cells first, then the feeds of new STANDBY INs.
	sel, reqs := s.selectAfter(s.assignChange(c, p.Chain, release), nil)
	var outs []string
	for _, o := range released {
		outs = append(outs, o.DestinationID)
	}
	outs = append(outs, c.DestinationID)
	if err := s.realize(ctx, c.ID, outs, sel, reqs); err != nil {
		return apitypes.MatrixCell{}, s.rollBack(ctx, affected, attempt, describe, err)
	}

	for _, o := range released {
		next := *o
		next.State, next.PresetID, next.Health, next.LKG = apitypes.ExecutionStateOff, nil, nil, nil
		if err := s.touch(ctx, &next); err != nil {
			return apitypes.MatrixCell{}, domain.Internal(err)
		}
		*o = next
		s.log(apitypes.LogLevelInfo, fmt.Sprintf("%s: released (OFF) for %s", o.ID, c.ID), o.ID, "")
	}
	next := *c
	next.PresetID = ptr(p.Id)
	if next.State == apitypes.ExecutionStateOff {
		next.State = apitypes.ExecutionStateStandby
	}
	s.markGood(&next)
	if err := s.touch(ctx, &next); err != nil {
		return apitypes.MatrixCell{}, domain.Internal(err)
	}
	*c = next
	s.orch.Adopt(sel)
	s.refresh(ctx)
	s.log(apitypes.LogLevelInfo, fmt.Sprintf("%s: preset '%s' applied", c.ID, p.Name), c.ID, "")
	s.sync()
	return s.view(c), nil
}

// ClearCellPreset turns a cell OFF and removes its preset. An on-air cell stops its output; with hot
// standby a STANDBY cell's IN stops being sent into the switchers.
// It is clearCellPreset in api/openapi.yaml.
type ClearCellPreset struct{ env *env }

// Run runs the use case.
func (u ClearCellPreset) Run(ctx context.Context, cellID string) (apitypes.MatrixCell, error) {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cells[cellID]
	if !ok {
		return apitypes.MatrixCell{}, domain.NotFound("Cell")
	}
	if c.State == apitypes.ExecutionStateOff || c.State == apitypes.ExecutionStateDisconnected {
		return s.view(c), nil
	}
	sel, reqs := s.selectAfter(change{}.then(map[string]apitypes.ExecutionState{c.ID: apitypes.ExecutionStateOff}), nil)
	if err := s.realize(ctx, c.ID, []string{c.DestinationID}, sel, reqs); err != nil {
		return apitypes.MatrixCell{}, s.rollBack(ctx, []*ssot.ActiveIntent{c}, nil, "clear", err)
	}
	next := *c
	next.State, next.PresetID, next.Health, next.LKG = apitypes.ExecutionStateOff, nil, nil, nil
	if err := s.touch(ctx, &next); err != nil {
		return apitypes.MatrixCell{}, domain.Internal(err)
	}
	*c = next
	s.orch.Adopt(sel)
	s.refresh(ctx)
	s.log(apitypes.LogLevelInfo, c.ID+": disconnected (OFF)", c.ID, "")
	s.sync()
	return s.view(c), nil
}

// TakeCell puts a standby cell on air; the OUT's previous on-air cell steps down to standby. The
// stream is allocated with make-before-break (its VVF blocks avoid nodes running another config),
// the Plan runs southbound (VVF configs first, then paths and switchers), and only then does the
// state change. A failure rolls the OUT back to the LKG. Afterwards the previous stream's policy is
// withdrawn or, with hot standby, turned back into a feed.
// It is takeCell in api/openapi.yaml.
type TakeCell struct{ env *env }

// Run runs the use case.
func (u TakeCell) Run(ctx context.Context, cellID string) (apitypes.TakeResponse, error) {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cells[cellID]
	if !ok {
		return apitypes.TakeResponse{}, domain.NotFound("Cell")
	}
	if c.State != apitypes.ExecutionStateStandby {
		return apitypes.TakeResponse{}, domain.Fail(apitypes.ErrorCodeINVALIDCELLSTATE, "%s is not in standby state", c.ID)
	}
	if issues := s.takeIssues(c); len(issues) > 0 {
		return apitypes.TakeResponse{}, &domain.Error{Code: apitypes.ErrorCodeRESOURCEEXHAUSTED, Message: issues[0].Message,
			Details: map[string]any{"issues": issues}}
	}
	var previous *ssot.ActiveIntent
	for _, id := range s.cellOrder {
		if o := s.cells[id]; o.DestinationID == c.DestinationID && o.State == apitypes.ExecutionStateActive {
			previous = o
		}
	}
	sel, reqs := s.selectAfter(takeChange(c), map[string]bool{c.DestinationID: true})
	plan, err := s.orch.RenderRealize(c.DestinationID, requestPtr(reqs, c.DestinationID), sel.SFPs[c.DestinationID], true)
	if err != nil {
		return apitypes.TakeResponse{}, planError(err)
	}
	if err := s.run(ctx, c.ID, plan.Instructions); err != nil {
		return apitypes.TakeResponse{}, s.rollBack(ctx, []*ssot.ActiveIntent{c}, nil, "take", err)
	}
	if previous != nil {
		prev := *previous
		prev.State = apitypes.ExecutionStateStandby
		if err := s.touch(ctx, &prev); err != nil {
			return apitypes.TakeResponse{}, domain.Internal(err)
		}
		*previous = prev
	}
	next := *c
	next.State = apitypes.ExecutionStateActive
	s.markGood(&next)
	if err := s.touch(ctx, &next); err != nil {
		return apitypes.TakeResponse{}, domain.Internal(err)
	}
	*c = next
	s.orch.Adopt(sel)
	s.refresh(ctx)
	if err := s.run(ctx, c.ID, plan.Release); err != nil {
		// The new stream is on air; a policy left behind only wastes the old path (the startup
		// Assurance repairs it).
		s.log(apitypes.LogLevelWarn, c.ID+": releasing the previous path failed", c.ID, "")
	}

	msg := "Take executed: " + c.ID + " is now ACTIVE"
	switched := []apitypes.SwitcherState{}
	for _, in := range plan.Instructions {
		if in.Op == domain.OpSwitcherSelect {
			switched = append(switched, apitypes.SwitcherState{SwitcherId: in.Target, ActiveInput: ptr(in.Select.Port)})
		}
	}
	resp := apitypes.TakeResponse{ActivatedCell: c.ID, Switchers: &switched}
	if previous != nil {
		msg += ", " + previous.ID + " demoted to STANDBY"
		resp.DeactivatedCell = ptr(previous.ID)
	}
	resp.Message = &msg
	s.log(apitypes.LogLevelInfo, fmt.Sprintf("%s (%d southbound instructions)", msg, len(plan.Instructions)), c.ID, "")
	s.sync()
	return resp, nil
}

// StandbyCell stops an on-air cell's output; it keeps its preset (with hot standby its IN keeps being
// sent into the switchers).
// It is standbyCell in api/openapi.yaml.
type StandbyCell struct{ env *env }

// Run runs the use case.
func (u StandbyCell) Run(ctx context.Context, cellID string) (apitypes.MatrixCell, error) {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cells[cellID]
	if !ok {
		return apitypes.MatrixCell{}, domain.NotFound("Cell")
	}
	if c.State != apitypes.ExecutionStateActive {
		return apitypes.MatrixCell{}, domain.Fail(apitypes.ErrorCodeINVALIDCELLSTATE, "%s is not on air", c.ID)
	}
	sel, reqs := s.selectAfter(change{}.then(map[string]apitypes.ExecutionState{c.ID: apitypes.ExecutionStateStandby}), nil)
	if err := s.realize(ctx, c.ID, []string{c.DestinationID}, sel, reqs); err != nil {
		return apitypes.MatrixCell{}, s.rollBack(ctx, []*ssot.ActiveIntent{c}, nil, "standby", err)
	}
	next := *c
	next.State = apitypes.ExecutionStateStandby
	if err := s.touch(ctx, &next); err != nil {
		return apitypes.MatrixCell{}, domain.Internal(err)
	}
	*c = next
	s.orch.Adopt(sel)
	s.refresh(ctx)
	s.log(apitypes.LogLevelInfo, c.ID+" stopped output and is now STANDBY", c.ID, "")
	s.sync()
	return s.view(c), nil
}

// GetCellPath returns the cell's real path for diagnostics: the headend, the switchers and VVF nodes of
// its stream (for a standby cell, those it would use if taken now; a block without a free node is
// `unassigned`), the egress, and the Segment List the stream runs (or would run).
// It is getCellPath in api/openapi.yaml.
type GetCellPath struct{ env *env }

// Run runs the use case.
func (u GetCellPath) Run(cellID string) (apitypes.CellPath, error) {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cells[cellID]
	if !ok {
		return apitypes.CellPath{}, domain.NotFound("Cell")
	}
	if c.State == apitypes.ExecutionStateOff || c.State == apitypes.ExecutionStateDisconnected {
		return apitypes.CellPath{}, domain.Fail(apitypes.ErrorCodeINVALIDCELLSTATE, "%s has no active path", c.ID)
	}
	sa := s.orch.SFP(c.DestinationID)
	if c.State != apitypes.ExecutionStateActive {
		sel, _ := s.selectAfter(takeChange(c), nil)
		sa = sel.SFPs[c.DestinationID]
	}
	healthy := ptr(apitypes.HealthStateHealthy)
	fill := func(pn *apitypes.PathNode, nodeID string) {
		n, ok := s.res.Node(nodeID)
		if !ok {
			return
		}
		pn.Label = n.Label
		if n.RouterID != "" {
			pn.RouterId = ptr(n.RouterID)
		}
		if len(n.SIDs) > 0 {
			pn.Sid = ptr(n.SIDs[0])
		}
	}
	src, _ := s.res.Source(c.SourceID)
	dst, _ := s.res.Destination(c.DestinationID)
	head := apitypes.PathNode{NodeId: c.SourceID, Label: c.SourceID, Role: apitypes.PathNodeRoleHeadend, HealthState: healthy}
	fill(&head, src.NodeID)
	nodes := []apitypes.PathNode{head}
	for _, h := range sa.Way {
		pn := apitypes.PathNode{NodeId: h.Node, Label: h.Node, Role: apitypes.PathNodeRoleVvf, HealthState: healthy}
		fill(&pn, h.Node)
		if f, ok := s.res.Flow(h.Node); ok {
			pn.VvfId = ptr(f.VVFID)
			if f.Behavior == domain.BehaviorSelect {
				pn.Switcher = &struct {
					InputPort  string `json:"input_port"`
					SwitcherId string `json:"switcher_id"`
				}{InputPort: h.Port, SwitcherId: h.Node}
			}
		}
		// The input port's SID, not the node's first one.
		if sid, err := s.orch.InputSID(h); err == nil {
			pn.Sid = ptr(sid)
		}
		nodes = append(nodes, pn)
	}
	chain := s.chainOf(c)
	complete := true
	req := s.tr.Translate(s.intentOf(c, chain, c.State == apitypes.ExecutionStateActive))
	for i, b := range req.SFs {
		ids := make([]string, len(b.Functions))
		for j, fn := range b.Functions {
			ids[j] = fn.InstanceId
		}
		pn := apitypes.PathNode{NodeId: b.VVF.Id, VvfId: ptr(b.VVF.Id), Role: apitypes.PathNodeRoleVvf, Label: b.VVF.Name,
			InstanceIds: &ids, HealthState: c.Health}
		if i < len(sa.Nodes) && sa.Nodes[i] != "" {
			pn.NodeId = sa.Nodes[i]
			fill(&pn, sa.Nodes[i])
		} else {
			complete = false
			pn.Unassigned = ptr(true)
		}
		nodes = append(nodes, pn)
	}
	egress := apitypes.PathNode{NodeId: c.DestinationID, Label: c.DestinationID, Role: apitypes.PathNodeRoleEgress, HealthState: healthy}
	fill(&egress, dst.NodeID)
	nodes = append(nodes, egress)
	hops := make([]apitypes.PathHop, 0, len(nodes)-1)
	for i := 0; i+1 < len(nodes); i++ {
		hops = append(hops, apitypes.PathHop{From: nodes[i].NodeId, To: nodes[i+1].NodeId})
	}
	path := apitypes.CellPath{CellId: c.ID, Nodes: nodes, Hops: hops}
	if complete {
		if segs, err := s.orch.SegmentList(req, sa); err == nil {
			path.SegmentList = ptr(segs)
		}
	}
	return path, nil
}

// toMap converts an API model to a JSON object for ErrorResponse.details.
func toMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}
