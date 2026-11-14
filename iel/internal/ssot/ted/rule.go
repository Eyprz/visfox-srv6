package ted

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/domain/tedyang"
)

// SID interpretation rules (Rule.Name). Both take roles from the advertised host names.
const (
	// RuleD4STestbed reads the current testbed convention `fd77:XY::N` (X: node group, Y: stream or
	// switcher input port, N: headend route index).
	RuleD4STestbed = "d4s-testbed"
	// RuleRFC8986 reads structured SIDs (RFC 8986 LOC:FUNCT:ARG, structure advertised as in
	// RFC 9514): one locator per node, whose End SID is the node SID PolaPCE routes with (not a
	// service SID), and the service SIDs (VVF functions, switcher input ports, decap) in it. Node ids
	// come from the locator's node part.
	RuleRFC8986 = "rfc8986"
)

// BehaviorEnd is the endpoint behavior of a node SID (RFC 8986 End, with any flavor).
const BehaviorEnd = "End"

// Rule is the SID interpretation rule shared with the lower layer (network operator config).
type Rule struct {
	// Name selects the interpreter: RuleD4STestbed or RuleRFC8986.
	Name string `yaml:"name"`
	// Roles map advertised nodes to roles; the first match wins.
	Roles []RoleRule `yaml:"roles"`
	// Arg is what the ARG block of the SIDs carries: "egress" (the OUT's arg on the egress SID: it
	// identifies the terminating node), "stream" (the IN's arg on every SID, the current testbed's
	// `::N`) or "none" (default).
	Arg string `yaml:"arg"`
}

// RoleRule classifies the nodes whose host name contains Match (case-insensitive).
type RoleRule struct {
	Match string      `yaml:"match"`
	Role  domain.Role `yaml:"role"`
	// VVFID is the VVF definition id for RoleVVF.
	VVFID string `yaml:"vvf_id"`
	// IDPrefix starts the derived node id (e.g. "caption" -> "caption-fd77-ba").
	IDPrefix string `yaml:"id_prefix"`
	// Label is the display name; "{n}" becomes the 1-based index among the nodes of this rule.
	Label string `yaml:"label"`
	// InputSIDs: every SID of the node is one input port (a switcher), lettered by rank; the id
	// drops the port digit.
	InputSIDs bool `yaml:"input_sids"`
}

// Binding attaches an IN / OUT to the advertised host name of its headend / decap node.
type Binding struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	VideoFormat string `yaml:"video_format"`
	Node        string `yaml:"node"`
	// Arg is the SID argument of the IN / OUT (see Rule.Arg).
	Arg uint64 `yaml:"arg"`
}

// Bindings are the IN / OUT attachments and display names from the network operator config.
type Bindings struct {
	Sources      []Binding `yaml:"sources"`
	Destinations []Binding `yaml:"destinations"`
	// Labels replace the rule's label for some nodes (host name -> display name), e.g. the material a
	// pinned caption node burns in. Names only: ids and roles still come from the advertisement.
	Labels map[string]string `yaml:"labels"`
}

// Interpret derives the TED (roles, ids, labels, switcher inputs, IN / OUT) from the YANG instance.
func Interpret(dev *tedyang.Device, rule Rule, b Bindings) (domain.TED, error) {
	if rule.Name != RuleD4STestbed && rule.Name != RuleRFC8986 {
		return domain.TED{}, fmt.Errorf("unknown SID interpretation rule %q", rule.Name)
	}
	switch rule.Arg {
	case "", "none", "stream", "egress":
	default:
		return domain.TED{}, fmt.Errorf("SID interpretation rule: unknown arg %q", rule.Arg)
	}
	nw := network(dev)
	if nw == nil {
		return domain.TED{}, fmt.Errorf("network %q not found in the TED", NetworkID)
	}

	var t domain.TED
	byRule := map[int][]*domain.Node{}
	byHost := map[string]string{}
	sidOwner := map[string]string{}

	for _, name := range sortedKeys(nw.Node) {
		yn := nw.Node[name]
		host, router := name, ""
		var sids []netip.Addr
		var locator *locatorShape
		if s := yn.Srv6; s != nil {
			if s.Hostname != nil {
				host = *s.Hostname
			}
			if s.RouterId != nil {
				router = *s.RouterId
			}
			for _, v := range sortedKeys(s.Sid) {
				a, err := netip.ParseAddr(v)
				if err != nil || !a.Is6() {
					return domain.TED{}, fmt.Errorf("node %q: SID %q is not an IPv6 address", name, v)
				}
				if rule.Name == RuleRFC8986 {
					shape, err := sidShape(s.Sid[v])
					if err != nil {
						return domain.TED{}, fmt.Errorf("node %q SID %s: %w", name, v, err)
					}
					locator = &shape
					if b := s.Sid[v].Behavior; b != nil && *b == BehaviorEnd {
						continue // the node SID: transit, not a service of the node
					}
				}
				sids = append(sids, a)
			}
		}
		slices.SortFunc(sids, func(a, b netip.Addr) int { return a.Compare(b) })

		ri := matchRule(rule.Roles, host)
		if ri < 0 {
			t.Unclassified = append(t.Unclassified, host)
			continue
		}
		rr := rule.Roles[ri]
		var id string
		var err error
		if rule.Name == RuleRFC8986 {
			id, err = locatorID(rr, host, sids, locator)
		} else {
			id, err = nodeID(rr, host, sids)
		}
		if err != nil {
			return domain.TED{}, fmt.Errorf("node %q: %w", name, err)
		}
		n := &domain.Node{ID: id, Role: rr.Role, Hostname: host, RouterID: router, VVFID: rr.VVFID}
		for i, a := range sids {
			sid := a.String()
			if owner, dup := sidOwner[sid]; dup {
				return domain.TED{}, fmt.Errorf("SID %s is advertised by both %q and %q", sid, owner, host)
			}
			sidOwner[sid] = host
			n.SIDs = append(n.SIDs, sid)
			if rr.InputSIDs {
				// `next` is the rank of the input SIDs (gaps collapse); port = 'a' + next.
				n.Inputs = append(n.Inputs, domain.InputPort{Port: string(rune('a' + i)), Next: i, SID: sid})
			}
		}
		if prev, dup := byHost[host]; dup {
			return domain.TED{}, fmt.Errorf("host name %q is advertised twice (%s)", host, prev)
		}
		byHost[host] = id
		byRule[ri] = append(byRule[ri], n)
	}

	seen := map[string]bool{}
	for ri := range rule.Roles {
		nodes := byRule[ri]
		slices.SortFunc(nodes, func(a, b *domain.Node) int { return cmp.Compare(a.ID, b.ID) })
		for i, n := range nodes {
			if seen[n.ID] {
				return domain.TED{}, fmt.Errorf("derived node id %q is not unique", n.ID)
			}
			seen[n.ID] = true
			n.Label = strings.ReplaceAll(rule.Roles[ri].Label, "{n}", strconv.Itoa(i+1))
			if l, ok := b.Labels[n.Hostname]; ok {
				n.Label = l
			}
			t.Nodes = append(t.Nodes, *n)
		}
	}

	for host := range b.Labels {
		if _, ok := byHost[host]; !ok {
			return domain.TED{}, fmt.Errorf("label for %q: no such node in the TED", host)
		}
	}
	var err error
	if t.Sources, err = bind(b.Sources, byHost, "IN"); err != nil {
		return domain.TED{}, err
	}
	if t.Destinations, err = bind(b.Destinations, byHost, "OUT"); err != nil {
		return domain.TED{}, err
	}
	return t, nil
}

func network(dev *tedyang.Device) *tedyang.IETFNetwork_Networks_Network {
	if dev == nil || dev.Networks == nil {
		return nil
	}
	return dev.Networks.Network[NetworkID]
}

func matchRule(rules []RoleRule, host string) int {
	h := strings.ToLower(host)
	for i, r := range rules {
		if r.Match != "" && strings.Contains(h, strings.ToLower(r.Match)) {
			return i
		}
	}
	return -1
}

// nodeID derives a stable id from the advertisement (never a sequence number):
// <prefix>-<locator>-<function>, where a node with input SIDs drops its input port (the last
// function digit).
// Nodes without SIDs (headends) use their host name.
func nodeID(rr RoleRule, host string, sids []netip.Addr) (string, error) {
	prefix := rr.IDPrefix
	if prefix == "" {
		prefix = string(rr.Role)
	}
	if len(sids) == 0 {
		return prefix + "-" + slug(host), nil
	}
	b := sids[0].As16()
	loc := fmt.Sprintf("%x", uint16(b[0])<<8|uint16(b[1]))
	fn := fmt.Sprintf("%x", uint16(b[2])<<8|uint16(b[3]))
	if rr.InputSIDs {
		if len(fn) < 2 {
			return "", fmt.Errorf("input SID %s has no port digit", sids[0])
		}
		fn = fn[:len(fn)-1]
	}
	return prefix + "-" + loc + "-" + fn, nil
}

// locatorShape is the advertised split of a SID: locator block and node lengths in bits.
type locatorShape struct {
	block, node uint8
}

func sidShape(sid *tedyang.IETFNetwork_Networks_Network_Node_Srv6_Sid) (locatorShape, error) {
	st := sid.Structure
	if st == nil || st.LocatorBlockLength == nil || st.LocatorNodeLength == nil {
		return locatorShape{}, fmt.Errorf("the %s rule needs the advertised SID structure", RuleRFC8986)
	}
	sh := locatorShape{block: *st.LocatorBlockLength, node: *st.LocatorNodeLength}
	if sh.node == 0 || sh.node > 64 || int(sh.block)+int(sh.node) > 128 {
		return locatorShape{}, fmt.Errorf("SID structure: locator block %d / node %d bits", sh.block, sh.node)
	}
	return sh, nil
}

// locatorID derives a stable id from the locator (RFC 8986): <prefix>-<node part in hex>. Nodes
// with no service SID (headends) use their host name.
func locatorID(rr RoleRule, host string, sids []netip.Addr, shape *locatorShape) (string, error) {
	prefix := rr.IDPrefix
	if prefix == "" {
		prefix = string(rr.Role)
	}
	if len(sids) == 0 || shape == nil {
		return prefix + "-" + slug(host), nil
	}
	node := bits(sids[0], shape.block, shape.node)
	for _, a := range sids[1:] {
		if bits(a, shape.block, shape.node) != node {
			return "", fmt.Errorf("SIDs %s and %s are in different locators", sids[0], a)
		}
	}
	return fmt.Sprintf("%s-%x", prefix, node), nil
}

// bits returns n bits of a starting at bit off (n <= 64).
func bits(a netip.Addr, off, n uint8) uint64 {
	b := a.As16()
	var v uint64
	for i := uint(off); i < uint(off)+uint(n); i++ {
		v = v<<1 | uint64(b[i/8]>>(7-i%8)&1)
	}
	return v
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func bind(bs []Binding, byHost map[string]string, kind string) ([]domain.Endpoint, error) {
	out := make([]domain.Endpoint, 0, len(bs))
	for _, x := range bs {
		id, ok := byHost[x.Node]
		if !ok {
			return nil, fmt.Errorf("%s %s: node %q is not in the TED", kind, x.ID, x.Node)
		}
		out = append(out, domain.Endpoint{ID: x.ID, Name: x.Name, VideoFormat: x.VideoFormat, NodeID: id, Arg: x.Arg})
	}
	return out, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
