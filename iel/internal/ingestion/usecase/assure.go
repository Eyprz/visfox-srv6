package usecase

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/assurance/compliance"
	"github.com/eyprz/visfox-srv6/iel/internal/assurance/monitor"
	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
	"github.com/eyprz/visfox-srv6/iel/internal/translation"
)

// AssureNetwork runs one pass of Intent Assurance (RFC 9315 §5.2) while the IEL runs: it follows the
// TED (BGP-LS) and reads the network back, compares it with the SSoT and, by the drift_action
// setting, repairs the drift or only reports it. It is no operation of api/openapi.yaml: cmd/visfox
// runs it every assurance_interval_s (dynamic config).
type AssureNetwork struct{ env *env }

// Interval is the configured assurance interval; 0 runs no pass (only the one at startup).
func (u AssureNetwork) Interval() time.Duration {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Duration(s.config.AssuranceIntervalS) * time.Second
}

// Run runs one pass. The network is read without holding the SSoT, so that operations are not held
// up by slow routers; a pass whose reading overlapped a change of the network is dropped (the next
// pass sees the result).
func (u AssureNetwork) Run(ctx context.Context) {
	s := u.env
	s.mu.Lock()
	gen, switchers := s.gen, s.switcherIDs()
	s.mu.Unlock()
	var res *ssot.Resources
	var tedErr error
	if s.readTED != nil {
		res, tedErr = s.readTED(ctx)
	}
	observing := s.monitor != nil || s.switches != nil
	var obs monitor.Observation
	var err error
	if observing {
		obs, err = s.observe(ctx, switchers)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		return
	}
	if s.readTED != nil {
		s.follow(ctx, res, tedErr)
	}
	if observing && s.gen == gen {
		s.assess(ctx, obs, err, false)
	}
}

// follow applies a TED read again from its source (BGP-LS): when it changed, the resources are
// rebuilt, the streams planned again on them (an on-air stream through a node that disappeared moves
// to another one) and the clients told (topology_changed). The OUTs whose path changed are then
// provisioned or reported by drift_action, as drift. A TED that cannot be read or interpreted (e.g.
// the headend of an IN disappeared) is reported and the current one kept.
func (s *env) follow(ctx context.Context, res *ssot.Resources, readErr error) {
	if readErr != nil {
		msg := "TED: cannot follow the TED source, keeping the current TED: " + readErr.Error()
		if msg != s.assurance.tedErr {
			s.log(apitypes.LogLevelWarn, msg, "", "")
		}
		s.assurance.tedErr = msg
		return
	}
	if s.assurance.tedErr != "" {
		s.log(apitypes.LogLevelInfo, "TED: the TED source can be followed again", "", "")
		s.assurance.tedErr = ""
	}
	if res == nil || reflect.DeepEqual(res.TED, s.res.TED) {
		return
	}
	added, removed := tedChange(s.res.TED, res.TED)
	before := map[string]domain.SFP{}
	for _, dst := range s.res.Destinations {
		before[dst.Id] = s.orch.SFP(dst.Id)
	}
	s.res = res
	s.cat = s.baseCat.WithNodes(res.VVFNodes)
	s.tr = translation.Translator{Catalog: s.cat}
	s.orch.SetResources(res)
	if s.saveTED != nil {
		if err := s.saveTED(ctx, res.TED); err != nil {
			s.log(apitypes.LogLevelError, "TED: storing the new TED failed: "+err.Error(), "", string(apitypes.ErrorCodeINTERNALERROR))
		}
	}
	s.log(apitypes.LogLevelWarn, fmt.Sprintf("TED: the topology changed (added %v, removed %v); planning the streams again", added, removed), "", "")
	s.refresh(ctx)
	moved := map[string]bool{}
	for _, dst := range s.res.Destinations {
		if !reflect.DeepEqual(before[dst.Id], s.orch.SFP(dst.Id)) {
			moved[dst.Id] = true
		}
	}
	s.publish(string(apitypes.TopologyChangedEventTypeTopologyChanged), apitypes.TopologyChangedEvent{
		Type: apitypes.TopologyChangedEventTypeTopologyChanged, Added: added, Removed: removed,
	})
	if len(moved) == 0 {
		s.sync()
		return
	}
	if s.config.DriftAction == apitypes.DriftActionReport {
		s.markDrift(ctx, moved)
		s.sync()
		return
	}
	s.repair(ctx, monitor.Observation{}, s.requests(change{}), moved)
}

// tedChange lists the node ids that appeared in and disappeared from the TED.
func tedChange(old, cur domain.TED) (added, removed []string) {
	ids := func(t domain.TED) map[string]bool {
		out := map[string]bool{}
		for _, n := range t.Nodes {
			out[n.ID] = true
		}
		return out
	}
	o, c := ids(old), ids(cur)
	for _, id := range slices.Sorted(maps.Keys(c)) {
		if !o[id] {
			added = append(added, id)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(o)) {
		if !c[id] {
			removed = append(removed, id)
		}
	}
	if added == nil {
		added = []string{}
	}
	if removed == nil {
		removed = []string{}
	}
	return added, removed
}

// observe reads the network: the SR Policies from the PCE and the inputs of the switcher VVFs.
func (s *env) observe(ctx context.Context, switchers []string) (monitor.Observation, error) {
	var sw monitor.SwitcherSource
	if s.switches != nil {
		sw = s.switches
	}
	return monitor.Observe(ctx, s.monitor, sw, switchers, s.readTimeout, s.now())
}

// switcherIDs are the switcher VVF nodes of the TED.
func (s *env) switcherIDs() []string {
	var ids []string
	for _, sw := range s.res.Switchers {
		ids = append(ids, sw.Id)
	}
	return ids
}

// assure runs Intent Assurance at startup: the network may have changed while the IEL was down.
func (s *env) assure(ctx context.Context) {
	if s.monitor == nil && s.switches == nil {
		return
	}
	obs, err := s.observe(ctx, s.switcherIDs())
	s.assess(ctx, obs, err, true)
}

// assess compares the observed network with the SSoT (compliance assessment) and acts on the drift
// (compliance action). The SR Policies the SSoT needs (on-air paths and, with hot standby, feeds)
// are compared with those the headends hold, the inputs the on-air ways need with those the
// switchers send on. With drift_action repair every OUT that drifted is provisioned again from the
// SSoT and IEL policies the SSoT does not have are withdrawn; with report the on-air cells of those
// OUTs turn warning until the network matches again. Only changes are logged, except at startup.
func (s *env) assess(ctx context.Context, obs monitor.Observation, readErr error, startup bool) {
	if readErr != nil {
		msg := "Assurance: cannot read the network: " + readErr.Error()
		if startup || s.assurance.readErr != msg {
			s.log(apitypes.LogLevelWarn, msg, "", "")
		}
		s.assurance.readErr = msg
		return
	}
	if s.assurance.readErr != "" {
		s.log(apitypes.LogLevelInfo, "Assurance: the network can be read again", "", "")
		s.assurance.readErr = ""
	}
	if unread := describeUnread(obs.Unread); unread != s.assurance.unread {
		if unread != "" {
			s.log(apitypes.LogLevelWarn, "Assurance: cannot read switcher VVFs: "+unread, "", "")
		}
		s.assurance.unread = unread
	}

	reqs := s.requests(change{})
	var d compliance.Drift
	if s.monitor != nil {
		expected, err := s.orch.Desired(reqs)
		if err != nil {
			s.log(apitypes.LogLevelError, "Assurance: cannot render the SSoT: "+err.Error(), "", string(apitypes.ErrorCodeINTERNALERROR))
			return
		}
		d = compliance.Assess(expected, obs.Policies)
	}
	sel, err := s.orch.Selections(reqs)
	if err != nil {
		s.log(apitypes.LogLevelError, "Assurance: cannot render the switcher inputs: "+err.Error(), "", string(apitypes.ErrorCodeINTERNALERROR))
		return
	}
	d.AssessSwitchers(sel, obs.Selections)

	// The OUTs that drifted.
	drifted := map[string]bool{}
	colors := d.Colors()
	for _, dst := range s.res.Destinations {
		if slices.Contains(colors, s.orch.Color(dst.Id)) {
			drifted[dst.Id] = true
		}
	}
	for _, sw := range d.Switchers {
		drifted[sw.DestinationID] = true
	}

	if d.None() {
		if startup || s.assurance.drift != "" {
			s.log(apitypes.LogLevelInfo, "Assurance: the network matches the SSoT", "", "")
		}
		s.assurance.drift = ""
		s.markDrift(ctx, drifted)
		return
	}
	report := s.config.DriftAction == apitypes.DriftActionReport
	if desc := d.String(); startup || !report || desc != s.assurance.drift {
		action := "provisioning the SSoT again"
		if report {
			action = "reported only (drift_action: report)"
		}
		s.log(apitypes.LogLevelWarn, "Assurance: drift from the SSoT: "+desc+"; "+action, "", "")
		s.assurance.drift = desc
	}
	if report {
		s.markDrift(ctx, drifted)
		return
	}
	s.assurance.drift = ""
	s.repair(ctx, obs, reqs, drifted)
}

// repair provisions every drifted OUT again from the SSoT, against the network as it is.
func (s *env) repair(ctx context.Context, obs monitor.Observation, reqs []domain.SFCRequest, drifted map[string]bool) {
	if obs.Policies != nil {
		s.orch.Observe(obs.Policies)
	}
	for _, dst := range s.res.Destinations {
		if !drifted[dst.Id] {
			continue
		}
		onAir := s.onAir(dst.Id)
		if onAir != nil {
			onAir.Health = ptr(apitypes.HealthStateRemediating)
			s.storeHealth(ctx, onAir)
			s.sync()
		}
		ins, err := s.orch.RenderRestore(dst.Id, requestPtr(reqs, dst.Id))
		ok := err == nil
		if ok {
			ok = s.run(ctx, cellIDOf(onAir), ins) == nil
		}
		if onAir != nil {
			onAir.Health = ptr(apitypes.HealthStateHealthy)
			msg, level := onAir.ID+": repaired the drift from the SSoT", apitypes.LogLevelWarn
			if !ok {
				onAir.Health = ptr(apitypes.HealthStateCritical)
				msg, level = onAir.ID+": repairing the drift from the SSoT failed", apitypes.LogLevelError
			}
			s.storeHealth(ctx, onAir)
			s.log(level, msg, onAir.ID, "")
		} else if !ok {
			s.log(apitypes.LogLevelError, dst.Id+": withdrawing SR Policies the SSoT does not have failed", "", "")
		}
	}
	s.refresh(ctx)
	s.sync()
}

// markDrift turns the on-air cells of the drifted OUTs warning (drift_action: report), and those of
// OUTs that match the SSoT again back to healthy.
func (s *env) markDrift(ctx context.Context, drifted map[string]bool) {
	changed := false
	for _, dst := range s.res.Destinations {
		c := s.onAir(dst.Id)
		if c == nil {
			continue
		}
		switch {
		case drifted[dst.Id] && (c.Health == nil || *c.Health == apitypes.HealthStateHealthy):
			c.Health = ptr(apitypes.HealthStateWarning)
			s.log(apitypes.LogLevelWarn, c.ID+": the network drifted from the SSoT (not repaired: drift_action report)", c.ID, "")
		case !drifted[dst.Id] && c.Health != nil && *c.Health == apitypes.HealthStateWarning:
			c.Health = ptr(apitypes.HealthStateHealthy)
			s.log(apitypes.LogLevelInfo, c.ID+": the network matches the SSoT again", c.ID, "")
		default:
			continue
		}
		s.storeHealth(ctx, c)
		changed = true
	}
	if changed {
		s.sync()
	}
}

// onAir is the OUT's on-air cell, nil when nothing is on air.
func (s *env) onAir(destinationID string) *ssot.ActiveIntent {
	for _, id := range s.cellOrder {
		if c := s.cells[id]; c.DestinationID == destinationID && c.State == apitypes.ExecutionStateActive {
			return c
		}
	}
	return nil
}

// storeHealth writes a cell (its health) to the SSoT.
func (s *env) storeHealth(ctx context.Context, c *ssot.ActiveIntent) {
	if err := s.touch(ctx, c); err != nil {
		s.log(apitypes.LogLevelError, c.ID+": storing the health failed: "+err.Error(), c.ID, "")
	}
}

func cellIDOf(c *ssot.ActiveIntent) string {
	if c == nil {
		return ""
	}
	return c.ID
}

// describeUnread lists the switchers that could not be read, in order.
func describeUnread(unread map[string]error) string {
	var parts []string
	for _, id := range slices.Sorted(maps.Keys(unread)) {
		parts = append(parts, fmt.Sprintf("%s (%v)", id, unread[id]))
	}
	return strings.Join(parts, ", ")
}
