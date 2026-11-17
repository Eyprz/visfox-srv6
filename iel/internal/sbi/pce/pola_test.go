package pce

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"testing"

	pb "github.com/nttcom/pola/api/pola/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

type fakePola struct {
	pb.UnimplementedPCEServiceServer
	created []*pb.CreateSRPolicyRequest
	deleted []*pb.DeleteSRPolicyRequest
}

func (f *fakePola) CreateSRPolicy(_ context.Context, r *pb.CreateSRPolicyRequest) (*pb.CreateSRPolicyResponse, error) {
	f.created = append(f.created, r)
	return &pb.CreateSRPolicyResponse{}, nil
}

func (f *fakePola) GetSRPolicyList(_ context.Context, _ *pb.GetSRPolicyListRequest) (*pb.GetSRPolicyListResponse, error) {
	var pols []*pb.SRPolicy
	for _, c := range f.created {
		r := c.SrPolicy
		segs := r.GetCandidatePath().GetExplicit().GetSegmentList()
		if dyn := r.GetCandidatePath().GetDynamic(); dyn != nil {
			// Pola reaches every waypoint through a transit node segment.
			for _, w := range dyn.GetWaypoints() {
				segs = append(segs, &pb.Segment{Sid: "fd00:0:1::"}, &pb.Segment{Sid: w.GetSid()})
			}
		}
		pols = append(pols, &pb.SRPolicy{PeerAddr: r.PeerAddr, Headend: r.Headend, Endpoint: r.Endpoint, Color: r.Color,
			PolicyName: r.PolicyName, SegmentList: segs, CandidatePath: r.CandidatePath})
	}
	return &pb.GetSRPolicyListResponse{Sessions: []*pb.SRPolicySession{
		{PeerAddr: netip.MustParseAddr("172.31.77.11").AsSlice(), SrPolicies: pols},
	}}, nil
}

func (f *fakePola) DeleteSRPolicy(_ context.Context, r *pb.DeleteSRPolicyRequest) (*pb.DeleteSRPolicyResponse, error) {
	f.deleted = append(f.deleted, r)
	return &pb.DeleteSRPolicyResponse{}, nil
}

var testAddrs = map[string]NodeAddress{
	"h1":    {PCEPPeer: netip.MustParseAddr("172.31.77.11"), Address: netip.MustParseAddr("fd77:e0::a")},
	"decap": {Address: netip.MustParseAddr("fd77:ef::1")},
}

// newTestClient connects a client with the options to a fake Pola.
func newTestClient(t *testing.T, o Options) (*Client, *fakePola) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	fake := &fakePola{}
	pb.RegisterPCEServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	p, err := New("passthrough:///bufnet", o,
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, fake
}

func TestClient(t *testing.T) {
	p, fake := newTestClient(t, Options{ASN: 65000, Nodes: testAddrs})

	pol := &domain.SRPolicy{Name: "visfox-in-1-out-1-decap", Headend: "h1", Endpoint: "decap", Color: 101,
		SegmentList: []string{"fd77:aa::", "fd77:ca::", "fd77:ff::1"}}
	ctx := context.Background()
	if err := p.Apply(ctx, domain.Instruction{Op: domain.OpSRPolicySet, Policy: pol}); err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(ctx, domain.Instruction{Op: domain.OpSRPolicyDelete, Policy: pol}); err != nil {
		t.Fatal(err)
	}
	if len(fake.created) != 1 || len(fake.deleted) != 1 {
		t.Fatalf("created %d deleted %d", len(fake.created), len(fake.deleted))
	}
	got := fake.created[0]
	peer, _ := netip.AddrFromSlice(got.SrPolicy.PeerAddr)
	end, _ := netip.AddrFromSlice(got.SrPolicy.Endpoint)
	segs := got.SrPolicy.CandidatePath.GetExplicit().GetSegmentList()
	if got.Asn != 65000 || !got.NoSidValidate || peer.String() != "172.31.77.11" || end.String() != "fd77:ef::1" || got.SrPolicy.Color != 101 ||
		len(segs) != 3 || segs[2].Sid != "fd77:ff::1" {
		t.Fatalf("request = %v", got)
	}

	// Monitoring reads the policies back with TED node ids.
	got2, err := p.SRPolicies(ctx)
	if err != nil || len(got2) != 1 || got2[0].Headend != "h1" || got2[0].Endpoint != "decap" || got2[0].Color != 101 ||
		len(got2[0].SegmentList) != 3 || got2[0].Name != pol.Name {
		t.Fatalf("observed = %+v, %v", got2, err)
	}

	// A node without addresses is a config error, not a call.
	bad := *pol
	bad.Headend = "h9"
	if err := p.Apply(ctx, domain.Instruction{Op: domain.OpSRPolicySet, Policy: &bad}); err == nil {
		t.Fatal("unknown headend accepted")
	}

	// A policy read back with the address of an endpoint without a node id (another network's) is
	// withdrawn by that address.
	foreign := *pol
	foreign.Endpoint = "fd77:ef::9"
	if err := p.Apply(ctx, domain.Instruction{Op: domain.OpSRPolicyDelete, Policy: &foreign}); err != nil {
		t.Fatal(err)
	}
	if end, _ := netip.AddrFromSlice(fake.deleted[1].SrPolicy.Endpoint); end.String() != "fd77:ef::9" {
		t.Fatalf("deleted endpoint = %v", end)
	}
	foreign.Endpoint = "decap-x"
	if err := p.Apply(ctx, domain.Instruction{Op: domain.OpSRPolicyDelete, Policy: &foreign}); err == nil {
		t.Fatal("unknown endpoint accepted")
	}
}

// With dynamic paths, a policy that reaches its endpoint asks Pola for a path through its SIDs as
// waypoints; a feed that ends before the endpoint stays explicit. Read back, a dynamic policy shows
// its waypoints as the Segment List and Pola's path as Computed.
func TestClientDynamicPath(t *testing.T) {
	routers := map[string]string{
		"decap": "10.255.4.1", "fd00:0:51::": "10.255.4.1",
		"fd00:0:21:a::": "10.255.1.1", "fd00:0:31:a::": "10.255.2.1",
	}
	p, fake := newTestClient(t, Options{ASN: 65000, Nodes: testAddrs, Dynamic: true, Routers: routers})
	ctx := context.Background()
	full := &domain.SRPolicy{Name: "visfox-full", Headend: "h1", Endpoint: "decap", Color: 101,
		SegmentList: []string{"fd00:0:21:a::", "fd00:0:31:a::", "fd00:0:51::"}}
	feed := &domain.SRPolicy{Name: "visfox-feed", Headend: "h1", Endpoint: "decap", Color: 102,
		SegmentList: []string{"fd00:0:21:a::"}}
	for _, pol := range []*domain.SRPolicy{full, feed} {
		if err := p.Apply(ctx, domain.Instruction{Op: domain.OpSRPolicySet, Policy: pol}); err != nil {
			t.Fatal(err)
		}
	}
	dyn := fake.created[0].SrPolicy.GetCandidatePath().GetDynamic()
	if dyn == nil || dyn.Metric != pb.MetricType_METRIC_TYPE_IGP || dyn.DataPlane != pb.DataPlane_DATA_PLANE_SRV6 ||
		len(dyn.Waypoints) != 3 || dyn.Waypoints[1].RouterId != "10.255.2.1" || dyn.Waypoints[1].Sid != "fd00:0:31:a::" {
		t.Fatalf("full path = %v", fake.created[0].SrPolicy.GetCandidatePath())
	}
	if fake.created[1].SrPolicy.GetCandidatePath().GetExplicit() == nil {
		t.Fatalf("feed = %v", fake.created[1].SrPolicy.GetCandidatePath())
	}

	got, err := p.SRPolicies(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("observed = %+v, %v", got, err)
	}
	if !slices.Equal(got[0].SegmentList, full.SegmentList) || len(got[0].Computed) != 6 || got[1].Computed != nil {
		t.Fatalf("observed = %+v", got)
	}

	bad := *full
	bad.SegmentList = []string{"fd00:0:99:a::", "fd00:0:51::"}
	if err := p.Apply(ctx, domain.Instruction{Op: domain.OpSRPolicySet, Policy: &bad}); err == nil {
		t.Fatal("a SID on no router accepted")
	}
	if m, err := MetricType("delay"); err != nil || m != pb.MetricType_METRIC_TYPE_DELAY {
		t.Fatalf("metric = %v, %v", m, err)
	}
	if _, err := MetricType("fastest"); err == nil {
		t.Fatal("unknown metric accepted")
	}
}
