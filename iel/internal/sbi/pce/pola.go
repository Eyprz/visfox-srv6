// Package pce is the PCE NBI client of the SFC Provisioner: it installs SR Policies through
// PolaPCE's gRPC API (github.com/nttcom/pola, PCEService), which sends them to the headends over
// PCEP.
package pce

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	pb "github.com/nttcom/pola/api/pola/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// NodeAddress is how PolaPCE reaches a TED node: the PCEP session of a headend (its PCC address)
// and the node's SRv6 address (the headend's source, or the endpoint of a policy).
type NodeAddress struct {
	PCEPPeer netip.Addr
	Address  netip.Addr
}

// Options configure the client.
type Options struct {
	// ASN is passed with every request (Pola's TED is per AS).
	ASN uint32
	// Validate asks Pola to check the SIDs of explicit paths against its TED (BGP-LS). Not with
	// Dynamic: Pola does not check waypoint SIDs, and its TED may then hold node SIDs only (End),
	// which the SIDs of a feed are not.
	Validate bool
	// Nodes are the addresses of the TED node ids that take part in SR Policies.
	Nodes map[string]NodeAddress
	// Dynamic lets Pola compute the path of every policy that reaches its endpoint: each SID of the
	// Segment List becomes a waypoint on the router that advertises it, and Pola fills in the path
	// between them (Metric). Policies that end before the endpoint (STANDBY feeds) stay explicit.
	Dynamic bool
	Metric  pb.MetricType
	// Routers maps the TED's SIDs and node ids to the router ids Pola's TED knows them by (dynamic).
	Routers map[string]string
}

// Client installs SR Policies through PolaPCE, with explicit or dynamic (PCE-computed) paths. Pola
// updates a policy in place when the headend already has one with the same color and endpoint, also
// from an explicit to a dynamic path and back.
type Client struct {
	client pb.PCEServiceClient
	conn   *grpc.ClientConn
	opts   Options
}

// New connects to polad's gRPC server (host:port).
func New(target string, o Options, dial ...grpc.DialOption) (*Client, error) {
	dial = append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, dial...)
	conn, err := grpc.NewClient(target, dial...)
	if err != nil {
		return nil, fmt.Errorf("polapce %s: %w", target, err)
	}
	if o.Metric == pb.MetricType_METRIC_TYPE_UNSPECIFIED {
		o.Metric = pb.MetricType_METRIC_TYPE_IGP
	}
	return &Client{client: pb.NewPCEServiceClient(conn), conn: conn, opts: o}, nil
}

// MetricType reads a metric name of the network operator config ("igp", "te", "delay", "hopcount").
func MetricType(name string) (pb.MetricType, error) {
	v, ok := pb.MetricType_value["METRIC_TYPE_"+strings.ToUpper(name)]
	if !ok || name == "" {
		return 0, fmt.Errorf("unknown path metric %q", name)
	}
	return pb.MetricType(v), nil
}

// Ready reports what Pola still lacks to take the IEL's SR Policies: a PCEP session (up) to every
// headend of Nodes and, when it checks or computes paths (Validate, Dynamic), a TED with nodes. Right
// after Pola starts both fill in over a few seconds; an SR Policy sent before fails.
func (p *Client) Ready(ctx context.Context) error {
	sess, err := p.client.GetSessionList(ctx, &pb.GetSessionListRequest{})
	if err != nil {
		return err
	}
	up := map[netip.Addr]bool{}
	for _, s := range sess.GetSessions() {
		if a, ok := netip.AddrFromSlice(s.GetPeerAddr()); ok && s.GetState() == pb.SessionState_SESSION_STATE_UP {
			up[a.Unmap()] = true
		}
	}
	var missing []string
	for id, a := range p.opts.Nodes {
		if a.PCEPPeer.IsValid() && !up[a.PCEPPeer] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("no PCEP session with %s", strings.Join(missing, ", "))
	}
	if !p.opts.Validate && !p.opts.Dynamic {
		return nil
	}
	ted, err := p.client.GetTED(ctx, &pb.GetTEDRequest{})
	if err != nil {
		return err
	}
	if !ted.GetEnabled() {
		return errors.New("TED is disabled in Pola")
	}
	if len(ted.GetNodes()) == 0 {
		return errors.New("TED is empty in Pola")
	}
	return nil
}

// Wait polls Ready every poll until Pola is ready or ctx ends, and returns the last error then.
func (p *Client) Wait(ctx context.Context, poll time.Duration) error {
	for {
		err := p.Ready(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(poll):
		}
	}
}

// Close closes the gRPC connection.
func (p *Client) Close() error { return p.conn.Close() }

// Name implements provision.Driver.
func (*Client) Name() string { return "polapce" }

// Apply implements provision.Driver for sr_policy.set and sr_policy.delete.
func (p *Client) Apply(ctx context.Context, in domain.Instruction) error {
	if in.Policy == nil {
		return fmt.Errorf("%s without a policy", in.Op)
	}
	pol, err := p.policy(*in.Policy)
	if err != nil {
		return err
	}
	switch in.Op {
	case domain.OpSRPolicySet:
		_, err = p.client.CreateSRPolicy(ctx, &pb.CreateSRPolicyRequest{SrPolicy: pol, Asn: p.opts.ASN, NoSidValidate: !p.opts.Validate || p.opts.Dynamic})
	case domain.OpSRPolicyDelete:
		_, err = p.client.DeleteSRPolicy(ctx, &pb.DeleteSRPolicyRequest{SrPolicy: pol, Asn: p.opts.ASN})
	default:
		return fmt.Errorf("polapce does not handle %s", in.Op)
	}
	return err
}

func (p *Client) policy(sp domain.SRPolicy) (*pb.SRPolicy, error) {
	head, ok := p.opts.Nodes[sp.Headend]
	if !ok || !head.PCEPPeer.IsValid() || !head.Address.IsValid() {
		return nil, fmt.Errorf("no PCEP peer / address for headend %s (network operator config)", sp.Headend)
	}
	end, ok := p.opts.Nodes[sp.Endpoint]
	if !ok || !end.Address.IsValid() {
		// A policy read back from Pola keeps the address of an endpoint without a node id (e.g. one
		// another network installed); withdrawing it uses that address.
		a, err := netip.ParseAddr(sp.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("no address for endpoint %s (network operator config)", sp.Endpoint)
		}
		end.Address = a
	}
	path, err := p.path(sp)
	if err != nil {
		return nil, err
	}
	return &pb.SRPolicy{
		PeerAddr:      head.PCEPPeer.AsSlice(),
		Headend:       head.Address.AsSlice(),
		Endpoint:      end.Address.AsSlice(),
		Color:         sp.Color,
		PolicyName:    sp.Name,
		CandidatePath: path,
	}, nil
}

// path is the candidate path of a policy: dynamic when the client computes paths in Pola and the
// Segment List reaches the endpoint (its last SID is on the endpoint's router), explicit otherwise.
func (p *Client) path(sp domain.SRPolicy) (*pb.CandidatePath, error) {
	if p.opts.Dynamic && len(sp.SegmentList) > 0 {
		last := p.opts.Routers[sp.SegmentList[len(sp.SegmentList)-1]]
		if end := p.opts.Routers[sp.Endpoint]; end != "" && last == end {
			wps := make([]*pb.Waypoint, len(sp.SegmentList))
			for i, sid := range sp.SegmentList {
				r, ok := p.opts.Routers[sid]
				if !ok {
					return nil, fmt.Errorf("dynamic path of %s: SID %s is on no router of the TED", sp.Name, sid)
				}
				wps[i] = &pb.Waypoint{RouterId: r, Sid: sid}
			}
			return &pb.CandidatePath{Preference: 100, Path: &pb.CandidatePath_Dynamic{Dynamic: &pb.DynamicPath{
				Metric: p.opts.Metric, DataPlane: pb.DataPlane_DATA_PLANE_SRV6,
				UnderlayFamily: pb.AddressFamily_ADDRESS_FAMILY_IPV6, Waypoints: wps}}}, nil
		}
	}
	segs := make([]*pb.Segment, len(sp.SegmentList))
	for i, sid := range sp.SegmentList {
		segs[i] = &pb.Segment{Sid: sid}
	}
	return &pb.CandidatePath{Preference: 100, Path: &pb.CandidatePath_Explicit{Explicit: &pb.ExplicitPath{SegmentList: segs}}}, nil
}

// SRPolicies reads the SR Policies Pola holds on every PCEP session (GetSRPolicyList), as the
// headends reported them, with TED node ids (monitoring, internal/assurance). A headend or endpoint
// without a node id keeps its address. The Segment List of a dynamic path is the SIDs of its
// waypoints (what the IEL asked for); the one Pola computed is in Computed.
func (p *Client) SRPolicies(ctx context.Context) ([]domain.SRPolicy, error) {
	resp, err := p.client.GetSRPolicyList(ctx, &pb.GetSRPolicyListRequest{})
	if err != nil {
		return nil, err
	}
	byPeer, byAddr := map[netip.Addr]string{}, map[netip.Addr]string{}
	for id, a := range p.opts.Nodes {
		if a.PCEPPeer.IsValid() {
			byPeer[a.PCEPPeer] = id
		}
		if a.Address.IsValid() {
			byAddr[a.Address] = id
		}
	}
	name := func(raw []byte, ids map[netip.Addr]string) string {
		a, ok := netip.AddrFromSlice(raw)
		if !ok {
			return ""
		}
		a = a.Unmap()
		if id, ok := ids[a]; ok {
			return id
		}
		return a.String()
	}
	var out []domain.SRPolicy
	for _, sess := range resp.GetSessions() {
		for _, pol := range sess.GetSrPolicies() {
			segs := pol.GetSegmentList()
			if len(segs) == 0 {
				segs = pol.GetCandidatePath().GetExplicit().GetSegmentList()
			}
			list := make([]string, len(segs))
			for i, sg := range segs {
				list[i] = sg.GetSid()
			}
			var computed []string
			if wps := pol.GetCandidatePath().GetDynamic().GetWaypoints(); len(wps) > 0 {
				computed = list
				list = make([]string, len(wps))
				for i, w := range wps {
					list[i] = w.GetSid()
				}
			}
			peer := pol.GetPeerAddr()
			if len(peer) == 0 {
				peer = sess.GetPeerAddr()
			}
			out = append(out, domain.SRPolicy{Name: pol.GetPolicyName(), Headend: name(peer, byPeer),
				Endpoint: name(pol.GetEndpoint(), byAddr), Color: pol.GetColor(), SegmentList: list, Computed: computed})
		}
	}
	return out, nil
}
