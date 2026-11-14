package domain

import (
	"encoding/json"
	"slices"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"
)

// SFCRequest is what Intent Translation hands to Intent Orchestration: one OUT's stream as SF
// instances to place, independent of the nodes and SIDs (RFC 7665 SFC, RFC 9315 §5.1.2).
type SFCRequest struct {
	// IntentID is the crosspoint intent the request realizes.
	IntentID      string
	SourceID      string
	DestinationID string
	// SFs are the VVF instances of the chain in execution order.
	SFs []SFInstance
	// OnAir: the request's intent is on air (SourceID is switched to the OUT).
	OnAir bool
	// Standby are the INs of the OUT's STANDBY intents. With hot standby they are already sent into
	// the OUT's switchers, so a Take only switches (ssot.Limits.StandbyFeed).
	Standby []string
}

// Fed are the INs the request sends into the OUT's switchers: the on-air IN, and with hot standby
// the STANDBY ones too.
func (r SFCRequest) Fed(hot bool) []string {
	var out []string
	if r.OnAir {
		out = append(out, r.SourceID)
	}
	if hot {
		for _, s := range r.Standby {
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return out
}

// SFInstance is one VVF instance of a chain (one SR-aware End.AN node): consecutive functions of one
// VVF until a type repeats or the pinned node changes, sorted into the order the End.AN requires.
type SFInstance struct {
	VVF apitypes.VvfDefinition
	// Pinned is the node a pinned VVF instance must run on (from the preset); empty for dynamic VVFs,
	// whose node Intent Orchestration selects.
	Pinned    string
	Functions []apitypes.FunctionInstance
}

// Config is the config a VVF node runs for the instance: its function types and params. A node
// runs one config (pool sharing same_config), so only streams with an equal config share it.
func (s SFInstance) Config() string {
	pairs := make([][2]any, len(s.Functions))
	for i, fn := range s.Functions {
		params := map[string]any{}
		if fn.Params != nil {
			params = fn.Params
		}
		pairs[i] = [2]any{fn.Type, params}
	}
	raw, _ := json.Marshal(pairs)
	return string(raw)
}

// ConfigOf is the Config of the functions a function.set_params instruction carries.
func ConfigOf(fns []FunctionConfig) string {
	s := SFInstance{Functions: make([]apitypes.FunctionInstance, len(fns))}
	for i, f := range fns {
		s.Functions[i] = apitypes.FunctionInstance{InstanceId: f.InstanceID, Type: apitypes.FunctionType(f.Type), Params: f.Params}
	}
	return s.Config()
}

// Hop is a VVF node that controls the stream flow (a switcher, a branch) on a stream's way, and the
// input port the stream enters it on.
type Hop struct {
	Node string
	Port string
}

// UnmarshalJSON also reads hops stored before switchers became VVFs ({"Switcher", "Port"}).
func (h *Hop) UnmarshalJSON(b []byte) error {
	var v struct{ Node, Switcher, Port string }
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	h.Node, h.Port = v.Node, v.Port
	if h.Node == "" {
		h.Node = v.Switcher
	}
	return nil
}

// SFP is the Service Function Path Intent Orchestration selected for a request (RFC 7665): the way
// of its on-air IN through the VVFs that control the stream flow (upstream first) and one
// processing VVF node per SF instance ("" when none is free). Feeds are the ways of every IN sent
// toward the OUT (the on-air one and, with hot standby, the STANDBY ones): together they form one
// tree whose root, a select node, feeds the OUT.
type SFP struct {
	Way   []Hop
	Nodes []string
	Feeds map[string][]Hop `json:",omitempty"`
}

// UnmarshalJSON also reads SFPs stored before switchers became VVFs ("Switchers" for Way).
func (s *SFP) UnmarshalJSON(b []byte) error {
	var v struct {
		Way, Switchers []Hop
		Nodes          []string
		Feeds          map[string][]Hop
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	s.Way, s.Nodes, s.Feeds = v.Way, v.Nodes, v.Feeds
	if s.Way == nil {
		s.Way = v.Switchers
	}
	return nil
}
