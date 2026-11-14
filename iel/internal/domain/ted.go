// Package domain holds the pure IEL models.
package domain

// Role is what a TED node does in the SRv6 SFC domain, derived from its SIDs by the SID
// interpretation rule.
type Role string

// Node roles. A switcher is a VVF too (behavior select): what a VVF
// does with streams comes from its definition in the catalog, not from the role.
const (
	RoleHeadend Role = "headend"
	RoleVVF     Role = "vvf"
	RoleDecap   Role = "decap"
)

// Behavior is what a VVF does with the streams it receives (VVF definition in the catalog).
type Behavior string

// VVF behaviors.
const (
	// BehaviorProcess processes one stream into one (e.g. caption, transcode): the functions of a chain.
	BehaviorProcess Behavior = "process"
	// BehaviorSelect sends out one of the streams it receives on its input ports, per output (a
	// switcher): it controls which stream flows on.
	BehaviorSelect Behavior = "select"
	// BehaviorReplicate sends copies of the stream it receives to several places (a branch, RFC 9524
	// replication segment).
	BehaviorReplicate Behavior = "replicate"
)

// TED is the interpreted traffic engineering database: what the IEL knows about the domain.
type TED struct {
	Nodes        []Node
	Sources      []Endpoint
	Destinations []Endpoint
	// Unclassified lists advertised nodes that no rule matched; they are not used.
	Unclassified []string
}

// Node is a TED node with its role. IDs are derived deterministically from the advertisement.
type Node struct {
	ID       string
	Label    string
	Role     Role
	Hostname string
	RouterID string
	// VVFID is the VVF definition id for RoleVVF nodes.
	VVFID string
	// SIDs are the node's SIDs without the argument, in ascending order.
	SIDs []string
	// Inputs are the input ports of a VVF that receives streams on separate ports (e.g. a switcher),
	// one per SID (SID interpretation rule input_sids).
	Inputs []InputPort
}

// InputPort is one input port of a VVF node: its SID, port letter and the `next` index used to
// select it.
type InputPort struct {
	Port string
	Next int
	SID  string
}

// Endpoint is an IN (source) or OUT (destination) bound to its headend or decap node.
type Endpoint struct {
	ID          string
	Name        string
	VideoFormat string
	NodeID      string
	// Arg is the IN's or OUT's SID argument (0: none), placed by the ARG rule of the SID
	// interpretation rule.
	Arg uint64
}
