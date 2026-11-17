package domain

import (
	"fmt"
	"strings"
)

// Op is the kind of a southbound instruction.
type Op string

// Southbound instruction kinds. Drivers implement them; new kinds keep the Plan shape.
const (
	// OpSRPolicySet installs or updates an SR Policy on a headend (PolaPCE, PCEP).
	OpSRPolicySet Op = "sr_policy.set"
	// OpSRPolicyDelete withdraws an SR Policy.
	OpSRPolicyDelete Op = "sr_policy.delete"
	// OpSwitcherSelect selects the input a switcher sends out.
	OpSwitcherSelect Op = "switcher.select"
	// OpFunctionSetParams configures the functions of a VVF node.
	OpFunctionSetParams Op = "function.set_params"
)

// Instruction is one southbound step. Exactly one of the payloads matches Op.
type Instruction struct {
	Op Op
	// Target is the TED node id the instruction is for (headend, switcher or VVF node).
	Target string
	// Policy is the SR Policy (sr_policy.*).
	Policy *SRPolicy
	// Select is the switcher input (switcher.select).
	Select *SwitcherSelect
	// Functions is the VVF node config (function.set_params).
	Functions []FunctionConfig
	// Note says why the instruction is there, for logs (e.g. "swap main stream tail").
	Note string
	// NewStream marks an SR Policy that starts a stream not flowing yet (a cold standby Take): the
	// provisioner waits for it to reach the switchers (stream_settle_ms) before the switch.
	NewStream bool `json:",omitempty"`
}

// SRPolicy is an explicit-path SR Policy (RFC 9256). A headend identifies it by color and endpoint.
type SRPolicy struct {
	Name string
	// Headend and Endpoint are TED node ids; drivers resolve their addresses.
	Headend  string
	Endpoint string
	Color    uint32
	// SegmentList holds the SIDs with their arguments, in order.
	SegmentList []string
	// Computed is the Segment List the PCE computed through SegmentList as waypoints (dynamic path,
	// read back from the PCE; empty for explicit paths).
	Computed []string `json:",omitempty"`
}

// SwitcherSelect selects a switcher input: Next is the D4S index (0 = first input, swap off).
type SwitcherSelect struct {
	Port string
	Next int
}

// Selection is the input a switcher VVF (select) sends on for an OUT: what the SSoT needs, compared
// with what the switcher reports (Intent Assurance).
type Selection struct {
	DestinationID string
	Node          string
	Next          int
}

// FunctionConfig is one function of a VVF node with its parameters.
type FunctionConfig struct {
	InstanceID string
	Type       string
	Params     map[string]any
}

// Plan is what realizes a change of an OUT's streams, in order: for a Take the VVF configs, then
// the paths and switchers (the order of those depends on the switcher mode).
type Plan struct {
	CellID       string
	Instructions []Instruction
	// Release runs after the change succeeded and the state changed: feeds that changed (the
	// previous on-air stream stepping down) and the policies nothing uses any more.
	Release []Instruction
}

// Describe renders the instruction for logs.
func (in Instruction) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", in.Op, in.Target)
	switch {
	case in.Policy != nil:
		fmt.Fprintf(&b, " -> %s color %d [%s]", in.Policy.Endpoint, in.Policy.Color, strings.Join(in.Policy.SegmentList, ", "))
	case in.Select != nil:
		fmt.Fprintf(&b, " input %s (next %d)", in.Select.Port, in.Select.Next)
	case len(in.Functions) > 0:
		types := make([]string, len(in.Functions))
		for i, f := range in.Functions {
			types[i] = f.Type
		}
		fmt.Fprintf(&b, " [%s]", strings.Join(types, ", "))
	}
	if in.Note != "" {
		fmt.Fprintf(&b, " (%s)", in.Note)
	}
	return b.String()
}
