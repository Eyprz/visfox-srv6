package usecase

import (
	"context"
	"slices"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/assurance/report"
	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
)

// ---------------------------------------------------------------- resources

// GetSources returns the INs.
// It is getSources in api/openapi.yaml.
type GetSources struct{ env *env }

// Run runs the use case.
func (u GetSources) Run() []apitypes.SourceNode {
	s := u.env
	return clone(s.res.Sources)
}

// GetDestinations returns the OUTs.
// It is getDestinations in api/openapi.yaml.
type GetDestinations struct{ env *env }

// Run runs the use case.
func (u GetDestinations) Run() []apitypes.DestinationNode {
	s := u.env
	return clone(s.res.Destinations)
}

// GetSwitchers returns the switchers with their wired inputs. Stage A does not drive switchers, so no
// input is selected.
// It is getSwitchers in api/openapi.yaml.
type GetSwitchers struct{ env *env }

// Run runs the use case.
func (u GetSwitchers) Run() []apitypes.SwitcherNode {
	s := u.env
	out := clone(s.res.Switchers)
	if out == nil {
		out = []apitypes.SwitcherNode{}
	}
	return out
}

// GetResourcePools returns the resource pools with the streams using them: VVF nodes and switchers from the
// allocation, IN unicasts from the cells whose IN is sent (on air; with hot standby STANDBY too).
// It is getResourcePools in api/openapi.yaml.
type GetResourcePools struct{ env *env }

// Run runs the use case.
func (u GetResourcePools) Run() []apitypes.ResourcePool {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	streams := s.requests(change{})
	hot := s.res.Limits.HotStandby()
	use := func(destinationID string) []apitypes.PoolUse {
		st, ok := requestOf(streams, destinationID)
		if !ok {
			return nil
		}
		return []apitypes.PoolUse{{DestinationId: destinationID, CellId: st.IntentID, OnAir: st.OnAir}}
	}
	return s.res.Pools(func(kind, nodeID string) []apitypes.PoolUse {
		var uses []apitypes.PoolUse
		if kind == ssot.PoolSourceUnicast {
			// The cells whose IN is sent: on air, and with hot standby STANDBY too.
			for _, id := range s.cellOrder {
				c := s.cells[id]
				onAir := c.State == apitypes.ExecutionStateActive
				if c.SourceID == nodeID && (onAir || (hot && c.State == apitypes.ExecutionStateStandby)) {
					uses = append(uses, apitypes.PoolUse{DestinationId: c.DestinationID, CellId: c.ID, OnAir: onAir})
				}
			}
			return uses
		}
		for _, st := range streams {
			sa := s.orch.SFP(st.DestinationID)
			in := slices.Contains(sa.Nodes, nodeID)
			if kind == ssot.PoolVVFOutput || kind == ssot.PoolVVFInput {
				in = slices.ContainsFunc(sa.Way, func(h domain.Hop) bool { return h.Node == nodeID })
				for _, way := range sa.Feeds {
					in = in || slices.ContainsFunc(way, func(h domain.Hop) bool { return h.Node == nodeID })
				}
			}
			if in {
				uses = append(uses, use(st.DestinationID)...)
			}
		}
		return uses
	})
}

// GetTopology returns the L7 topology: IN, VVF nodes (processing and flow) and OUT, linked along the
// allocated way of every on-air cell and the feed of every STANDBY cell whose IN is sent (standby).
// It is getTopology in api/openapi.yaml.
type GetTopology struct{ env *env }

// Run runs the use case.
func (u GetTopology) Run() apitypes.Topology {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	healthy := ptr(apitypes.TopologyNodeStatusHealthy)
	status := func(online bool) *apitypes.TopologyNodeStatus {
		if online {
			return healthy
		}
		return ptr(apitypes.TopologyNodeStatusOffline)
	}
	t := apitypes.Topology{Nodes: []apitypes.TopologyNode{}, Links: []apitypes.TopologyLink{}}
	for _, src := range s.res.Sources {
		t.Nodes = append(t.Nodes, apitypes.TopologyNode{Id: src.Id, Label: src.Name, Type: apitypes.TopologyNodeTypeSource,
			Status: status(src.Status == apitypes.SourceNodeStatusOnline)})
	}
	for _, n := range s.res.VVFNodes {
		types := []apitypes.FunctionType{}
		if v, ok := s.cat.VVF(n.VVFID); ok {
			types = clone(v.FunctionTypes)
		}
		t.Nodes = append(t.Nodes, apitypes.TopologyNode{Id: n.ID, Label: n.Label, Type: apitypes.TopologyNodeTypeVvf,
			VvfId: ptr(n.VVFID), FunctionTypes: &types, Status: healthy})
	}
	// The VVFs that control the stream flow (switchers, branches) have no functions.
	for _, f := range s.res.FlowNodes {
		t.Nodes = append(t.Nodes, apitypes.TopologyNode{Id: f.ID, Label: f.Label, Type: apitypes.TopologyNodeTypeVvf,
			VvfId: ptr(f.VVFID), FunctionTypes: &[]apitypes.FunctionType{}, Status: healthy})
	}
	for _, dst := range s.res.Destinations {
		t.Nodes = append(t.Nodes, apitypes.TopologyNode{Id: dst.Id, Label: dst.Name, Type: apitypes.TopologyNodeTypeDestination,
			Status: status(dst.Status == apitypes.DestinationNodeStatusOnline)})
	}
	// On-air cells: IN -> flow nodes (switchers) -> VVF nodes -> OUT, as allocated. STANDBY cells
	// whose IN is sent into the switchers (hot standby): IN -> flow nodes, up to the switcher that
	// would select it. Wiring nothing flows on is not a link.
	type key struct {
		source, target string
		standby        bool
	}
	links := map[key]int{}
	link := func(from, to, cellID string, standby bool) {
		k := key{from, to, standby}
		if i, ok := links[k]; ok {
			ids := append(*t.Links[i].CellIds, cellID)
			t.Links[i].CellIds = &ids
			return
		}
		l := apitypes.TopologyLink{Source: from, Target: to, CellIds: &[]string{cellID}, Standby: ptr(standby)}
		links[k] = len(t.Links)
		t.Links = append(t.Links, l)
	}
	for _, id := range s.cellOrder {
		c := s.cells[id]
		sa := s.orch.SFP(c.DestinationID)
		switch c.State {
		case apitypes.ExecutionStateActive:
			way := []string{c.SourceID}
			for _, h := range sa.Way {
				way = append(way, h.Node)
			}
			for _, n := range sa.Nodes {
				if n != "" {
					way = append(way, n)
				}
			}
			way = append(way, c.DestinationID)
			for i := 0; i+1 < len(way); i++ {
				link(way[i], way[i+1], c.ID, false)
			}
		case apitypes.ExecutionStateStandby:
			feed, ok := sa.Feeds[c.SourceID]
			if !ok || len(feed) == 0 {
				continue // not sent (cold standby, or no room)
			}
			from := c.SourceID
			for _, h := range feed {
				link(from, h.Node, c.ID, true)
				from = h.Node
			}
		}
	}
	return t
}

// ---------------------------------------------------------------- functions

// GetFunctions returns the function catalog.
// It is getFunctions in api/openapi.yaml.
type GetFunctions struct{ env *env }

// Run runs the use case.
func (u GetFunctions) Run() []apitypes.FunctionCatalogItem {
	s := u.env
	return clone(s.cat.Functions)
}

// GetFunctionSchema returns one catalog entry.
// It is getFunctionSchema in api/openapi.yaml.
type GetFunctionSchema struct{ env *env }

// Run runs the use case.
func (u GetFunctionSchema) Run(t apitypes.FunctionType) (apitypes.FunctionCatalogItem, error) {
	s := u.env
	f, ok := s.cat.Function(t)
	if !ok {
		return apitypes.FunctionCatalogItem{}, domain.NotFound("Function type")
	}
	return clone(f), nil
}

// ValidateFunctionParams checks params against a function's schema (range checks only).
// It is validateFunctionParams in api/openapi.yaml.
type ValidateFunctionParams struct{ env *env }

// Run runs the use case.
func (u ValidateFunctionParams) Run(t apitypes.FunctionType, params map[string]any) (apitypes.ParamValidationResult, error) {
	s := u.env
	if _, ok := s.cat.Function(t); !ok {
		return apitypes.ParamValidationResult{}, domain.NotFound("Function type")
	}
	errs := s.tr.CheckInterface(t, params)
	if errs == nil {
		errs = []apitypes.FieldError{}
	}
	return apitypes.ParamValidationResult{Valid: len(errs) == 0, Errors: &errs}, nil
}

// ---------------------------------------------------------------- config

// GetConfig returns the settings (immutable and dynamic).
// It is getConfig in api/openapi.yaml.
type GetConfig struct{ env *env }

// Run runs the use case.
func (u GetConfig) Run() apitypes.SystemConfig {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.systemConfig()
}

func (s *env) systemConfig() apitypes.SystemConfig {
	im := s.immutable
	return apitypes.SystemConfig{Immutable: &im, Dynamic: clone(s.config)}
}

// UpdateConfig changes the dynamic settings.
// It is updateConfig in api/openapi.yaml.
type UpdateConfig struct{ env *env }

// Run runs the use case.
func (u UpdateConfig) Run(ctx context.Context, p apitypes.DynamicConfigPatch) (apitypes.SystemConfig, error) {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.SseIntervalMs == nil && p.MetricsDumpIntervalS == nil && p.LogLevel == nil && p.DefaultSla == nil {
		return apitypes.SystemConfig{}, domain.Fail(apitypes.ErrorCodeINVALIDREQUEST, "At least one property is required")
	}
	var errs []apitypes.FieldError
	bad := func(field, msg string) { errs = append(errs, apitypes.FieldError{Field: ptr(field), Message: msg}) }
	if p.SseIntervalMs != nil && *p.SseIntervalMs < 100 {
		bad("sse_interval_ms", "Must be 100 or more")
	}
	if p.MetricsDumpIntervalS != nil && *p.MetricsDumpIntervalS < 10 {
		bad("metrics_dump_interval_s", "Must be 10 or more")
	}
	if p.LogLevel != nil {
		if !report.KnownLevel(*p.LogLevel) {
			bad("log_level", "Must be one of debug, info, warn, error")
		}
	}
	if len(errs) > 0 {
		return apitypes.SystemConfig{}, &domain.Error{Code: apitypes.ErrorCodeINVALIDPARAMS, Message: "Invalid config",
			Details: map[string]any{"errors": errs}}
	}
	next := s.config
	if p.SseIntervalMs != nil {
		next.SseIntervalMs = *p.SseIntervalMs
	}
	if p.MetricsDumpIntervalS != nil {
		next.MetricsDumpIntervalS = *p.MetricsDumpIntervalS
	}
	if p.LogLevel != nil {
		next.LogLevel = *p.LogLevel
	}
	if p.DefaultSla != nil {
		next.DefaultSla = p.DefaultSla
	}
	if err := s.store.PutDynamicConfig(ctx, next); err != nil {
		return apitypes.SystemConfig{}, domain.Internal(err)
	}
	s.config = next
	s.oplog.SetLevel(next.LogLevel)
	s.onLevel(next.LogLevel)
	s.publish(string(apitypes.ConfigChangedEventTypeConfigChanged), apitypes.ConfigChangedEvent{
		Type: apitypes.ConfigChangedEventTypeConfigChanged, Config: clone(next),
	})
	s.log(apitypes.LogLevelInfo, "System config updated", "", "")
	return s.systemConfig(), nil
}
