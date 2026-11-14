package bgpls

import (
	"context"
	"net"
	"slices"
	"testing"
	"time"

	api "github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/eyprz/visfox-srv6/iel/internal/ssot/ted"
)

var lsFamily = &api.Family{Afi: api.Family_AFI_LS, Safi: api.Family_SAFI_LS}

func desc(id string) *api.LsNodeDescriptor { return &api.LsNodeDescriptor{Asn: 65000, IgpRouterId: id} }

func lsPath(nlri *api.LsAddrPrefix_LsNLRI, typ api.LsNLRIType, attr *api.LsAttribute) *api.Path {
	return &api.Path{
		Family: lsFamily,
		Nlri: &api.NLRI{Nlri: &api.NLRI_LsAddrPrefix{LsAddrPrefix: &api.LsAddrPrefix{
			Type: typ, Nlri: nlri, ProtocolId: api.LsProtocolID_LS_PROTOCOL_ID_OSPF_V3,
		}}},
		Pattrs: []*api.Attribute{
			{Attr: &api.Attribute_Origin{Origin: &api.OriginAttribute{}}},
			{Attr: &api.Attribute_NextHop{NextHop: &api.NextHopAttribute{NextHop: "0.0.0.0"}}},
			{Attr: &api.Attribute_Ls{Ls: attr}},
		},
	}
}

func nodePath(id, name string) *api.Path {
	return lsPath(&api.LsAddrPrefix_LsNLRI{Nlri: &api.LsAddrPrefix_LsNLRI_Node{Node: &api.LsNodeNLRI{LocalNode: desc(id)}}},
		api.LsNLRIType_LS_NLRI_TYPE_NODE, &api.LsAttribute{Node: &api.LsAttributeNode{Name: name}})
}

func sidPath(id, sid string, behavior uint32) *api.Path {
	return lsPath(&api.LsAddrPrefix_LsNLRI{Nlri: &api.LsAddrPrefix_LsNLRI_Srv6Sid{Srv6Sid: &api.LsSrv6SIDNLRI{
		LocalNode: desc(id), Srv6SidInformation: &api.LsSrv6SIDInformation{Sids: []string{sid}},
	}}}, api.LsNLRIType_LS_NLRI_TYPE_SRV6_SID, &api.LsAttribute{Srv6Sid: &api.LsAttributeSrv6SID{
		Srv6EndpointBehavior: &api.LsSrv6EndpointBehavior{EndpointBehavior: behavior},
		Srv6SidStructure:     &api.LsSrv6SIDStructure{LocalBlock: 32, LocalNode: 16, LocalFunc: 16},
	}})
}

func linkPath(a, b string, n int) *api.Path {
	return lsPath(&api.LsAddrPrefix_LsNLRI{Nlri: &api.LsAddrPrefix_LsNLRI_Link{Link: &api.LsLinkNLRI{
		LocalNode: desc(a), RemoteNode: desc(b),
		LinkDescriptor: &api.LsLinkDescriptor{InterfaceAddrIpv6: "fd00:fe::" + string(rune('0'+n))},
	}}}, api.LsNLRIType_LS_NLRI_TYPE_LINK, &api.LsAttribute{Link: &api.LsAttributeLink{IgpMetric: 10}})
}

// The routes a BGP-LS collector holds become the TED seed: host names, SIDs in order with their
// behavior and structure, and one link per node pair.
func TestLoadReadsGoBGP(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	s := server.NewBgpServer(server.GrpcListenAddress(addr))
	go s.Serve()
	t.Cleanup(func() { _ = s.StopBgp(context.Background(), &api.StopBgpRequest{}) })

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	c := api.NewGoBgpServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ready := grpc.WaitForReady(true)
	if _, err := c.StartBgp(ctx, &api.StartBgpRequest{Global: &api.Global{Asn: 65000, RouterId: "10.0.0.254", ListenPort: -1}}, ready); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*api.Path{
		nodePath("10.0.0.1", "Switcher-1"),
		sidPath("10.0.0.1", "fd00:0:21:b::", 65535),
		sidPath("10.0.0.1", "fd00:0:21::", 1),
		sidPath("10.0.0.1", "fd00:0:21:a::", 65535),
		nodePath("10.0.0.2", "Decap-1"),
		sidPath("10.0.0.2", "fd00:0:51::", 18),
		linkPath("10.0.0.1", "10.0.0.2", 1),
		linkPath("10.0.0.2", "10.0.0.1", 2),
	} {
		if _, err := c.AddPath(ctx, &api.AddPathRequest{TableType: api.TableType_TABLE_TYPE_GLOBAL, Path: p}, ready); err != nil {
			t.Fatal(err)
		}
	}

	dev, err := Source{Target: addr, Wait: 5 * time.Second, Poll: 50 * time.Millisecond}.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nw := dev.Networks.Network[ted.NetworkID]
	sw := nw.Node["Switcher-1"]
	if sw == nil || *sw.Srv6.RouterId != "10.0.0.1" || len(sw.Srv6.Sid) != 3 {
		t.Fatalf("switcher = %+v", sw)
	}
	if b := sw.Srv6.Sid["fd00:0:21::"].Behavior; b == nil || *b != ted.BehaviorEnd {
		t.Fatalf("End SID behavior = %v", b)
	}
	if st := sw.Srv6.Sid["fd00:0:21:a::"].Structure; st == nil || *st.LocatorBlockLength != 32 || *st.LocatorNodeLength != 16 {
		t.Fatalf("structure = %+v", st)
	}
	if b := nw.Node["Decap-1"].Srv6.Sid["fd00:0:51::"].Behavior; b == nil || *b != "End.DT6" {
		t.Fatalf("decap behavior = %v", b)
	}
	if len(nw.Link) != 1 {
		t.Fatalf("links = %v", nw.Link)
	}
}

func TestSeedNamesAndOrders(t *testing.T) {
	seed, err := Seed([]*api.Path{
		sidPath("10.0.0.9", "fd00:0:9:b::", 65535),
		sidPath("10.0.0.9", "fd00:0:9:a::", 65535),
		nodePath("10.0.0.1", "Encap-A"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seed.Nodes) != 2 || seed.Nodes[0].Name != "Encap-A" || seed.Nodes[1].Name != "10.0.0.9" {
		t.Fatalf("nodes = %+v", seed.Nodes)
	}
	got := []string{seed.Nodes[1].SIDs[0].Value, seed.Nodes[1].SIDs[1].Value}
	if !slices.Equal(got, []string{"fd00:0:9:a::", "fd00:0:9:b::"}) || seed.Nodes[1].SIDs[0].Behavior != "Opaque" {
		t.Fatalf("sids = %+v", seed.Nodes[1].SIDs)
	}
}

func TestLoadGivesUpWithoutRoutes(t *testing.T) {
	_, err := Source{Target: "127.0.0.1:1", Wait: 200 * time.Millisecond, Poll: 50 * time.Millisecond}.Load(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
}
