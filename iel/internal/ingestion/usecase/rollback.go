package usecase

import (
	"context"
	"errors"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
)

// This file is the v1 compliance action for apply errors: a change whose
// provisioning failed is rolled back to the last known good config (LKG), which is what the SSoT
// still holds, because the SSoT changes only after provisioning succeeded.

// snapshot is a cell config as the API shows it in rollbacks (no L3 data).
func (s *env) snapshot(presetID string, chain []apitypes.FunctionInstance) apitypes.CellConfigSnapshot {
	snap := apitypes.CellConfigSnapshot{LiveChain: clone(chain)}
	if presetID != "" {
		snap.PresetId = ptr(presetID)
		if p := s.preset(presetID); p != nil {
			snap.PresetName = ptr(p.Name)
		}
	}
	if snap.LiveChain == nil {
		snap.LiveChain = []apitypes.FunctionInstance{}
	}
	return snap
}

// current is the cell's config in the SSoT.
func (s *env) current(c *ssot.ActiveIntent) apitypes.CellConfigSnapshot {
	id := ""
	if c.PresetID != nil {
		id = *c.PresetID
	}
	return s.snapshot(id, s.chainOf(c))
}

// markGood records the cell's config as its LKG after a change was provisioned without error.
func (s *env) markGood(c *ssot.ActiveIntent) {
	snap := s.current(c)
	c.LKG = &snap
	c.Health = ptr(apitypes.HealthStateHealthy)
}

// rollBack handles a failed provisioning (cause, from run): every affected cell is marked
// remediating, the network of every affected on-air OUT is restored to the SSoT (the LKG: the OUT's
// on-air intent on its current path, with the policies the failed change left withdrawn), and the
// cells end healthy or, when the restore failed or no LKG exists, critical. attempts are the
// configs the change tried (default: the cell's own). It returns the API error with the rollbacks.
func (s *env) rollBack(ctx context.Context, cells []*ssot.ActiveIntent, attempts map[string]apitypes.CellConfigSnapshot,
	describe string, cause error) error {
	code := apitypes.ErrorCodeROUTERERROR
	var de *domain.Error
	if errors.As(cause, &de) && de.Code == apitypes.ErrorCodeROUTERTIMEOUT {
		code = de.Code
	}
	reason := "Router returned an error while applying " + describe
	if code == apitypes.ErrorCodeROUTERTIMEOUT {
		reason = "Router timed out while applying " + describe
	}
	now := s.now()
	infos := make([]apitypes.RollbackInfo, len(cells))
	for i, c := range cells {
		from, ok := attempts[c.ID]
		if !ok {
			from = s.current(c)
		}
		infos[i] = apitypes.RollbackInfo{CellId: c.ID, Trigger: apitypes.RollbackInfoTriggerApplyError,
			Result: apitypes.RollbackInfoResultInProgress, ReasonCode: ptr(code), Reason: reason, From: from,
			To: c.LKG, OccurredAt: now}
		c.Health = ptr(apitypes.HealthStateRemediating)
		c.LastRollback = &infos[i]
		s.saveRollback(ctx, c)
	}
	s.sync()

	// Restore every affected OUT to the SSoT: its on-air intent, its feeds (hot standby), nothing else.
	restored := true
	seen := map[string]bool{}
	reqs := s.requests(change{})
	for _, c := range cells {
		if seen[c.DestinationID] {
			continue
		}
		seen[c.DestinationID] = true
		ins, err := s.orch.RenderRestore(c.DestinationID, requestPtr(reqs, c.DestinationID))
		if err != nil {
			s.log(apitypes.LogLevelError, c.ID+": cannot plan the rollback: "+err.Error(), c.ID, string(apitypes.ErrorCodeINTERNALERROR))
			restored = false
			continue
		}
		if err := s.run(ctx, c.ID, ins); err != nil {
			restored = false
		}
	}
	s.refresh(ctx)

	for i, c := range cells {
		// An OFF cell's known good state is OFF (it has no health).
		off := c.State == apitypes.ExecutionStateOff
		ok := restored && (c.LKG != nil || off)
		infos[i].Result = apitypes.RollbackInfoResultSucceeded
		c.Health = ptr(apitypes.HealthStateHealthy)
		if !ok {
			infos[i].Result = apitypes.RollbackInfoResultFailed
			c.Health = ptr(apitypes.HealthStateCritical)
		}
		if off {
			c.Health = nil
		}
		c.LastRollback = &infos[i]
		s.saveRollback(ctx, c)
		if ok {
			name := "none"
			if c.LKG != nil && c.LKG.PresetName != nil {
				name = *c.LKG.PresetName
			}
			s.log(apitypes.LogLevelWarn, c.ID+": "+reason+". Rolled back to the last known good config ("+name+").", c.ID, string(code))
		} else {
			s.log(apitypes.LogLevelError, c.ID+": "+reason+". Rollback to the last known good config failed.", c.ID, string(code))
		}
	}
	s.sync()
	return &domain.Error{Code: code, Message: reason + ": " + causeMessage(cause), Details: map[string]any{}, Rollbacks: infos}
}

// saveRollback stores the cell's health and rollback and publishes the rollback event.
func (s *env) saveRollback(ctx context.Context, c *ssot.ActiveIntent) {
	if err := s.touch(ctx, c); err != nil {
		s.log(apitypes.LogLevelError, c.ID+": storing the rollback failed: "+err.Error(), c.ID, string(apitypes.ErrorCodeINTERNALERROR))
	}
	s.publish(string(apitypes.RollbackEventTypeRollback), apitypes.RollbackEvent{
		Type: apitypes.RollbackEventTypeRollback, Rollback: *c.LastRollback,
	})
}

func causeMessage(err error) string {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Message
	}
	return err.Error()
}
