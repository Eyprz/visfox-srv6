// Package bgpls is the BGP-LS client of the TED pipeline: it reads
// the link-state routes a BGP-LS collector (GoBGP, gRPC API) holds and hands them on as the TED
// source's YANG instance, the same shape the static seed gives (ssot/ted Source). Nodes come from the
// node NLRIs (RFC 9552, host name attribute), SIDs with their behavior and structure from the SRv6
// SID NLRIs (RFC 9514), and links from the link NLRIs.
package bgpls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	api "github.com/osrg/gobgp/v4/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/eyprz/visfox-srv6/iel/internal/domain/tedyang"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot/ted"
)

// Source reads the TED from GoBGP's global RIB (address family link-state).
type Source struct {
	// Target is GoBGP's gRPC API (host:port).
	Target string
	// Wait is how long Load waits for the RIB to hold nodes (the collector may still be learning);
	// 0 means one minute.
	Wait time.Duration
	// Poll is the interval between reads while waiting; 0 means two seconds.
	Poll time.Duration
	// Dial adds gRPC dial options (tests).
	Dial []grpc.DialOption
}

// Load implements ted.Source: it waits until the RIB holds nodes and two reads in a row agree, then
// converts the routes.
func (s Source) Load(ctx context.Context) (*tedyang.Device, error) {
	opts := append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, s.Dial...)
	conn, err := grpc.NewClient(s.Target, opts...)
	if err != nil {
		return nil, fmt.Errorf("gobgp %s: %w", s.Target, err)
	}
	defer func() { _ = conn.Close() }()
	client := api.NewGoBgpServiceClient(conn)

	wait, poll := s.Wait, s.Poll
	if wait == 0 {
		wait = time.Minute
	}
	if poll == 0 {
		poll = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var prev []*api.Path
	var lastErr error
	for {
		paths, err := List(ctx, client)
		switch {
		case err != nil:
			lastErr = err
		case len(paths) == 0:
			lastErr = errors.New("no BGP-LS routes yet")
		case prev != nil && len(paths) == len(prev):
			seed, err := Seed(paths)
			if err != nil {
				return nil, err
			}
			return seed.ToYANG()
		default:
			prev = paths
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("gobgp %s: no stable BGP-LS TED within %s: %w", s.Target, wait, lastErr)
		case <-time.After(poll):
		}
	}
}

// List returns the best BGP-LS route of every destination in GoBGP's global RIB.
func List(ctx context.Context, c api.GoBgpServiceClient) ([]*api.Path, error) {
	stream, err := c.ListPath(ctx, &api.ListPathRequest{
		TableType: api.TableType_TABLE_TYPE_GLOBAL,
		Family:    &api.Family{Afi: api.Family_AFI_LS, Safi: api.Family_SAFI_LS},
	})
	if err != nil {
		return nil, err
	}
	var out []*api.Path
	for {
		r, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		paths := r.GetDestination().GetPaths()
		if i := slices.IndexFunc(paths, func(p *api.Path) bool { return p.GetBest() }); i >= 0 {
			out = append(out, paths[i])
		} else if len(paths) > 0 {
			out = append(out, paths[0])
		}
	}
}

// Seed converts BGP-LS routes into the TED seed: one node per IGP router id (named by its host name
// attribute, or the router id when it advertises none), its SRv6 SIDs, and one link per node pair.
// Other NLRI types (prefixes, SR Policies) are not part of the IEL's TED.
func Seed(paths []*api.Path) (ted.Seed, error) {
	type node struct {
		name string
		sids []ted.SeedSID
	}
	nodes := map[string]*node{}
	get := func(id string) *node {
		if nodes[id] == nil {
			nodes[id] = &node{}
		}
		return nodes[id]
	}
	type pair struct{ a, b string }
	links := map[pair]bool{}

	for _, p := range paths {
		if p.GetIsWithdraw() {
			continue
		}
		prefix := p.GetNlri().GetLsAddrPrefix()
		if prefix == nil {
			continue
		}
		attr := lsAttribute(p)
		switch nlri := prefix.GetNlri().GetNlri().(type) {
		case *api.LsAddrPrefix_LsNLRI_Node:
			id := nlri.Node.GetLocalNode().GetIgpRouterId()
			if id == "" {
				return ted.Seed{}, errors.New("BGP-LS node NLRI without an IGP router id")
			}
			get(id).name = attr.GetNode().GetName()
		case *api.LsAddrPrefix_LsNLRI_Srv6Sid:
			id := nlri.Srv6Sid.GetLocalNode().GetIgpRouterId()
			sa := attr.GetSrv6Sid()
			for _, v := range nlri.Srv6Sid.GetSrv6SidInformation().GetSids() {
				sid := ted.SeedSID{Value: v, Behavior: Behavior(sa.GetSrv6EndpointBehavior().GetEndpointBehavior())}
				if st := sa.GetSrv6SidStructure(); st != nil {
					shape, err := structure(st)
					if err != nil {
						return ted.Seed{}, fmt.Errorf("SID %s: %w", v, err)
					}
					sid.Structure = shape
				}
				get(id).sids = append(get(id).sids, sid)
			}
		case *api.LsAddrPrefix_LsNLRI_Link:
			a := nlri.Link.GetLocalNode().GetIgpRouterId()
			b := nlri.Link.GetRemoteNode().GetIgpRouterId()
			if a == "" || b == "" || a == b {
				continue
			}
			get(a)
			get(b)
			if b < a {
				a, b = b, a
			}
			links[pair{a, b}] = true
		}
	}

	name := func(id string) string {
		if n := nodes[id]; n != nil && n.name != "" {
			return n.name
		}
		return id
	}
	var seed ted.Seed
	for _, id := range sortedKeys(nodes) {
		n := nodes[id]
		slices.SortFunc(n.sids, func(a, b ted.SeedSID) int { return compare(a.Value, b.Value) })
		n.sids = slices.CompactFunc(n.sids, func(a, b ted.SeedSID) bool { return a.Value == b.Value })
		seed.Nodes = append(seed.Nodes, ted.SeedNode{Name: name(id), RouterID: id, SIDs: n.sids})
	}
	for _, l := range sortedKeys(links) {
		seed.Links = append(seed.Links, ted.SeedLink{Source: name(l.a), Destination: name(l.b)})
	}
	return seed, nil
}

func lsAttribute(p *api.Path) *api.LsAttribute {
	for _, a := range p.GetPattrs() {
		if ls := a.GetLs(); ls != nil {
			return ls
		}
	}
	return nil
}

func structure(st *api.LsSrv6SIDStructure) (*ted.SeedSIDShape, error) {
	out := &ted.SeedSIDShape{}
	for _, f := range []struct {
		v   uint32
		dst *uint8
	}{
		{st.GetLocalBlock(), &out.LocatorBlock}, {st.GetLocalNode(), &out.LocatorNode},
		{st.GetLocalFunc(), &out.Function}, {st.GetLocalArg(), &out.Argument},
	} {
		if f.v > 128 {
			return nil, fmt.Errorf("SID structure length %d exceeds 128 bits", f.v)
		}
		*f.dst = uint8(f.v)
	}
	return out, nil
}

// behaviors names the SRv6 Endpoint Behavior code points (IANA registry, RFC 8986). End with any
// flavor is "End", the node SID of the SID interpretation rule rfc8986.
var behaviors = map[uint32]string{
	1: ted.BehaviorEnd, 2: ted.BehaviorEnd, 3: ted.BehaviorEnd, 4: ted.BehaviorEnd,
	5: "End.X", 6: "End.X", 7: "End.X", 8: "End.X",
	9: "End.T", 10: "End.T", 11: "End.T", 12: "End.T",
	16: "End.DX6", 17: "End.DX4", 18: "End.DT6", 19: "End.DT4", 20: "End.DT46",
	65535: "Opaque",
}

// Behavior names an SRv6 Endpoint Behavior code point ("" for none, "behavior-N" when unknown).
func Behavior(code uint32) string {
	if code == 0 {
		return ""
	}
	if n, ok := behaviors[code]; ok {
		return n
	}
	return fmt.Sprintf("behavior-%d", code)
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func sortedKeys[K interface{ comparable }, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b K) int { return compare(fmt.Sprint(a), fmt.Sprint(b)) })
	return keys
}
