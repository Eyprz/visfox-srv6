// Package pce is the PCE NBI client of the SFC Provisioner: it installs SR Policies through
// PolaPCE's gRPC API (github.com/nttcom/pola, PCEService), which sends them to the headends over
// PCEP.
package pce

import (
	"context"
	"fmt"
	"net/netip"

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

// Client installs explicit-path SR Policies through PolaPCE. Pola updates a policy in place when the
// headend already has one with the same color and endpoint.
type Client struct {
	client pb.PCEServiceClient
	conn   *grpc.ClientConn
	asn    uint32
	// validate asks Pola to check the SIDs against its TED (BGP-LS). Off while the IEL's TED is not
	// the one Pola learns (v1: static TED).
	validate bool
	// nodes are the addresses of the TED node ids that take part in SR Policies.
	nodes map[string]NodeAddress
}

// New connects to polad's gRPC server (host:port). nodes are the addresses of the TED node ids
// (network operator config); validate makes Pola check the SIDs against its TED.
func New(target string, asn uint32, validate bool, nodes map[string]NodeAddress, opts ...grpc.DialOption) (*Client, error) {
	opts = append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, opts...)
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("polapce %s: %w", target, err)
	}
	return &Client{client: pb.NewPCEServiceClient(conn), conn: conn, asn: asn, validate: validate, nodes: nodes}, nil
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
		_, err = p.client.CreateSRPolicy(ctx, &pb.CreateSRPolicyRequest{SrPolicy: pol, Asn: p.asn, NoSidValidate: !p.validate})
	case domain.OpSRPolicyDelete:
		_, err = p.client.DeleteSRPolicy(ctx, &pb.DeleteSRPolicyRequest{SrPolicy: pol, Asn: p.asn})
	default:
		return fmt.Errorf("polapce does not handle %s", in.Op)
	}
	return err
}

func (p *Client) policy(sp domain.SRPolicy) (*pb.SRPolicy, error) {
	head, ok := p.nodes[sp.Headend]
	if !ok || !head.PCEPPeer.IsValid() || !head.Address.IsValid() {
		return nil, fmt.Errorf("no PCEP peer / address for headend %s (network operator config)", sp.Headend)
	}
	end, ok := p.nodes[sp.Endpoint]
	if !ok || !end.Address.IsValid() {
		// A policy read back from Pola keeps the address of an endpoint without a node id (e.g. one
		// another network installed); withdrawing it uses that address.
		a, err := netip.ParseAddr(sp.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("no address for endpoint %s (network operator config)", sp.Endpoint)
		}
		end.Address = a
	}
	segs := make([]*pb.Segment, len(sp.SegmentList))
	for i, sid := range sp.SegmentList {
		segs[i] = &pb.Segment{Sid: sid}
	}
	return &pb.SRPolicy{
		PeerAddr:   head.PCEPPeer.AsSlice(),
		Headend:    head.Address.AsSlice(),
		Endpoint:   end.Address.AsSlice(),
		Color:      sp.Color,
		PolicyName: sp.Name,
		CandidatePath: &pb.CandidatePath{
			Preference: 100,
			Path:       &pb.CandidatePath_Explicit{Explicit: &pb.ExplicitPath{SegmentList: segs}},
		},
	}, nil
}

// SRPolicies reads the SR Policies Pola holds on every PCEP session (GetSRPolicyList), as the
// headends reported them, with TED node ids (monitoring, internal/assurance). A headend or endpoint
// without a node id keeps its address.
func (p *Client) SRPolicies(ctx context.Context) ([]domain.SRPolicy, error) {
	resp, err := p.client.GetSRPolicyList(ctx, &pb.GetSRPolicyListRequest{})
	if err != nil {
		return nil, err
	}
	byPeer, byAddr := map[netip.Addr]string{}, map[netip.Addr]string{}
	for id, a := range p.nodes {
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
			peer := pol.GetPeerAddr()
			if len(peer) == 0 {
				peer = sess.GetPeerAddr()
			}
			out = append(out, domain.SRPolicy{Name: pol.GetPolicyName(), Headend: name(peer, byPeer),
				Endpoint: name(pol.GetEndpoint(), byAddr), Color: pol.GetColor(), SegmentList: list})
		}
	}
	return out, nil
}
