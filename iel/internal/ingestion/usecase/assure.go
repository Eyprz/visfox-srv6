package usecase

import (
	"context"
	"slices"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/assurance/compliance"
	"github.com/eyprz/visfox-srv6/iel/internal/assurance/monitor"
)

// assure compares the network with the SSoT (RFC 9315 §5.2: monitoring, compliance assessment) and
// repairs the drift (compliance action): the SR Policies the SSoT needs (on-air paths and, with hot
// standby, feeds) are compared with those the network holds; every OUT whose policies differ is
// provisioned again from the SSoT, and IEL policies the SSoT does not have are withdrawn. It runs at startup, when the
// network may have changed while the IEL was down. Without a monitor (dry run) it does nothing.
func (s *env) assure(ctx context.Context) {
	if s.monitor == nil {
		return
	}
	obs, err := monitor.Observe(ctx, s.monitor, s.now())
	if err != nil {
		s.log(apitypes.LogLevelWarn, "Assurance: cannot read the SR Policies from the network: "+err.Error(), "", "")
		return
	}
	reqs := s.requests(change{})
	expected, err := s.orch.Desired(reqs)
	if err != nil {
		s.log(apitypes.LogLevelError, "Assurance: cannot render the SSoT: "+err.Error(), "", string(apitypes.ErrorCodeINTERNALERROR))
		return
	}
	d := compliance.Assess(expected, obs.Policies)
	if d.None() {
		s.log(apitypes.LogLevelInfo, "Assurance: the network matches the SSoT", "", "")
		return
	}
	s.log(apitypes.LogLevelWarn, "Assurance: drift from the SSoT: "+d.String()+"; provisioning the SSoT again", "", "")

	// Restore against the network as it is.
	s.orch.Observe(obs.Policies)
	colors := d.Colors()
	for _, dst := range s.res.Destinations {
		if !slices.Contains(colors, s.orch.Color(dst.Id)) {
			continue
		}
		var onAir string
		for _, id := range s.cellOrder {
			if c := s.cells[id]; c.DestinationID == dst.Id && c.State == apitypes.ExecutionStateActive {
				onAir = c.ID
			}
		}
		ins, err := s.orch.RenderRestore(dst.Id, requestPtr(reqs, dst.Id))
		ok := err == nil
		if ok {
			ok = s.run(ctx, onAir, ins) == nil
		}
		if onAir != "" {
			c := s.cells[onAir]
			c.Health = ptr(apitypes.HealthStateHealthy)
			msg, level := onAir+": repaired the drift from the SSoT", apitypes.LogLevelWarn
			if !ok {
				c.Health = ptr(apitypes.HealthStateCritical)
				msg, level = onAir+": repairing the drift from the SSoT failed", apitypes.LogLevelError
			}
			if err := s.touch(ctx, c); err != nil {
				s.log(apitypes.LogLevelError, onAir+": storing the health failed: "+err.Error(), onAir, "")
			}
			s.log(level, msg, onAir, "")
		} else if !ok {
			s.log(apitypes.LogLevelError, dst.Id+": withdrawing SR Policies the SSoT does not have failed", "", "")
		}
	}
	s.refresh(ctx)
}
