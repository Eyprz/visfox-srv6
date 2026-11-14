// Command fakebgpls is a stand-in BGP-LS speaker for the local C-Plane lab (infra/cplane): it reads a
// TED file in the IEL's seed format (iel/db/ted-lab.yaml: nodes with router ids, loopbacks and SRv6
// SIDs, links with IGP metrics), originates it as BGP-LS routes (RFC 9552 node, link and prefix
// NLRIs, RFC 9514 SRv6 SID NLRIs) in an embedded GoBGP speaker and advertises them over an iBGP
// session to the lab's GoBGP, as a router exporting its IGP over BGP-LS would. PolaPCE (ted.source:
// gobgp, which follows routes received from peers) and the IEL read their TED from there. It is a
// test tool for VISFOX-SRv6, not part of the IEL.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	api "github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/server"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TED is the advertised topology (the IEL's TED seed format, internal/ssot/ted).
type TED struct {
	SIDStructure *Structure `yaml:"sid_structure"`
	Nodes        []Node     `yaml:"nodes"`
	Links        []Link     `yaml:"links"`
}

// Node is one router.
type Node struct {
	Name     string `yaml:"name"`
	RouterID string `yaml:"router_id"`
	Loopback string `yaml:"loopback"`
	SIDs     []SID  `yaml:"sids"`
}

// SID is one SRv6 SID of a node.
type SID struct {
	Value     string     `yaml:"value"`
	Behavior  string     `yaml:"behavior"`
	Structure *Structure `yaml:"structure"`
}

// Structure is the SRv6 SID structure in bits (RFC 9514).
type Structure struct {
	LocatorBlock uint32 `yaml:"locator_block"`
	LocatorNode  uint32 `yaml:"locator_node"`
	Function     uint32 `yaml:"function"`
	Argument     uint32 `yaml:"argument"`
}

// Link is a link between two nodes, advertised in both directions.
type Link struct {
	Source      string `yaml:"source"`
	Destination string `yaml:"destination"`
	Metric      uint32 `yaml:"metric"`
}

// UnmarshalYAML accepts a bare string as a SID value.
func (s *SID) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		s.Value = n.Value
		return nil
	}
	type plain SID
	return n.Decode((*plain)(s))
}

// behaviors are the SRv6 Endpoint Behavior code points (IANA, RFC 8986). End.AN (SR-aware VVF,
// draft-ietf-spring-sr-service-programming) has none and is advertised as Opaque.
var behaviors = map[string]uint32{
	"End": 1, "End.X": 5, "End.T": 9, "End.DX6": 16, "End.DX4": 17, "End.DT6": 18, "End.DT4": 19,
	"End.DT46": 20, "End.AN": 65535, "Opaque": 65535,
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fakebgpls stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	path := env("FAKEBGPLS_TED", "/ted.yaml")
	peer := env("FAKEBGPLS_PEER", "172.31.77.3")
	routerID := env("FAKEBGPLS_ROUTER_ID", "172.31.77.4")
	asn, err := strconv.ParseUint(env("FAKEBGPLS_ASN", "65000"), 10, 32)
	if err != nil {
		return fmt.Errorf("FAKEBGPLS_ASN: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var ted TED
	if err := yaml.Unmarshal(raw, &ted); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	paths, err := Paths(ted, uint32(asn))
	if err != nil {
		return err
	}
	// The embedded speaker, driven through its own gRPC API like gobgpd.
	speaker := server.NewBgpServer(server.GrpcListenAddress(localAPI))
	go speaker.Serve()
	conn, err := grpc.NewClient(localAPI, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	client := api.NewGoBgpServiceClient(conn)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ready := grpc.WaitForReady(true)

	if _, err := client.StartBgp(ctx, &api.StartBgpRequest{Global: &api.Global{
		Asn: uint32(asn), RouterId: routerID, ListenPort: -1,
	}}, ready); err != nil {
		return fmt.Errorf("start BGP: %w", err)
	}
	if _, err := client.AddPeer(ctx, &api.AddPeerRequest{Peer: &api.Peer{
		Conf:     &api.PeerConf{NeighborAddress: peer, PeerAsn: uint32(asn)},
		AfiSafis: []*api.AfiSafi{{Config: &api.AfiSafiConfig{Family: lsFamily, Enabled: true}}},
	}}, ready); err != nil {
		return fmt.Errorf("add peer %s: %w", peer, err)
	}
	for _, p := range paths {
		if _, err := client.AddPath(ctx, &api.AddPathRequest{TableType: api.TableType_TABLE_TYPE_GLOBAL, Path: p}, ready); err != nil {
			return fmt.Errorf("add BGP-LS route: %w", err)
		}
	}
	log.Info("BGP-LS routes originated", "ted", path, "nodes", len(ted.Nodes), "links", len(ted.Links),
		"routes", len(paths), "peer", peer)
	<-ctx.Done()
	return speaker.StopBgp(context.Background(), &api.StopBgpRequest{})
}

// localAPI is the embedded speaker's gRPC API (local only).
const localAPI = "127.0.0.1:50061"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var lsFamily = &api.Family{Afi: api.Family_AFI_LS, Safi: api.Family_SAFI_LS}

// Paths turns the TED into BGP-LS routes: per node a node NLRI (host name), a prefix NLRI (loopback)
// and an SRv6 SID NLRI per SID; per link a link NLRI in each direction (IGP metric, IPv6 interface
// addresses fd00:fe:<link>::1 / ::2). The IGP is OSPFv3 (router ids are dotted quads).
func Paths(ted TED, asn uint32) ([]*api.Path, error) {
	byName := map[string]Node{}
	for _, n := range ted.Nodes {
		if _, err := netip.ParseAddr(n.RouterID); err != nil {
			return nil, fmt.Errorf("node %s: router_id: %w", n.Name, err)
		}
		byName[n.Name] = n
	}
	desc := func(n Node) *api.LsNodeDescriptor {
		return &api.LsNodeDescriptor{Asn: asn, IgpRouterId: n.RouterID}
	}
	var out []*api.Path
	for _, n := range ted.Nodes {
		out = append(out, lsPath(&api.LsAddrPrefix_LsNLRI{Nlri: &api.LsAddrPrefix_LsNLRI_Node{
			Node: &api.LsNodeNLRI{LocalNode: desc(n)},
		}}, api.LsNLRIType_LS_NLRI_TYPE_NODE, &api.LsAttribute{Node: &api.LsAttributeNode{Name: n.Name}}))

		if n.Loopback != "" {
			lo, err := netip.ParseAddr(n.Loopback)
			if err != nil || !lo.Is6() {
				return nil, fmt.Errorf("node %s: loopback %q is not an IPv6 address", n.Name, n.Loopback)
			}
			out = append(out, lsPath(&api.LsAddrPrefix_LsNLRI{Nlri: &api.LsAddrPrefix_LsNLRI_PrefixV6{
				PrefixV6: &api.LsPrefixV6NLRI{LocalNode: desc(n), PrefixDescriptor: &api.LsPrefixDescriptor{
					IpReachability: []string{netip.PrefixFrom(lo, 128).String()},
				}},
			}}, api.LsNLRIType_LS_NLRI_TYPE_PREFIX_V6, &api.LsAttribute{Prefix: &api.LsAttributePrefix{IgpFlags: &api.LsIGPFlags{}}}))
		}

		for _, s := range n.SIDs {
			sid, err := netip.ParseAddr(s.Value)
			if err != nil || !sid.Is6() {
				return nil, fmt.Errorf("node %s: SID %q is not an IPv6 address", n.Name, s.Value)
			}
			code, ok := behaviors[s.Behavior]
			if !ok {
				return nil, fmt.Errorf("node %s: SID %s: unknown behavior %q", n.Name, s.Value, s.Behavior)
			}
			st := s.Structure
			if st == nil {
				st = ted.SIDStructure
			}
			attr := &api.LsAttributeSrv6SID{Srv6EndpointBehavior: &api.LsSrv6EndpointBehavior{EndpointBehavior: code}}
			if st != nil {
				attr.Srv6SidStructure = &api.LsSrv6SIDStructure{
					LocalBlock: st.LocatorBlock, LocalNode: st.LocatorNode, LocalFunc: st.Function, LocalArg: st.Argument,
				}
			}
			out = append(out, lsPath(&api.LsAddrPrefix_LsNLRI{Nlri: &api.LsAddrPrefix_LsNLRI_Srv6Sid{
				Srv6Sid: &api.LsSrv6SIDNLRI{LocalNode: desc(n), Srv6SidInformation: &api.LsSrv6SIDInformation{Sids: []string{sid.String()}}},
			}}, api.LsNLRIType_LS_NLRI_TYPE_SRV6_SID, &api.LsAttribute{Srv6Sid: attr}))
		}
	}

	for i, l := range ted.Links {
		a, ok := byName[l.Source]
		if !ok {
			return nil, fmt.Errorf("link %d: unknown node %q", i, l.Source)
		}
		b, ok := byName[l.Destination]
		if !ok {
			return nil, fmt.Errorf("link %d: unknown node %q", i, l.Destination)
		}
		metric := l.Metric
		if metric == 0 {
			metric = 1
		}
		addrA := fmt.Sprintf("fd00:fe:%x::1", i+1)
		addrB := fmt.Sprintf("fd00:fe:%x::2", i+1)
		for _, d := range []struct {
			local, remote       Node
			localIf, neighborIf string
		}{{a, b, addrA, addrB}, {b, a, addrB, addrA}} {
			out = append(out, lsPath(&api.LsAddrPrefix_LsNLRI{Nlri: &api.LsAddrPrefix_LsNLRI_Link{Link: &api.LsLinkNLRI{
				LocalNode:      desc(d.local),
				RemoteNode:     desc(d.remote),
				LinkDescriptor: &api.LsLinkDescriptor{InterfaceAddrIpv6: d.localIf, NeighborAddrIpv6: d.neighborIf},
			}}}, api.LsNLRIType_LS_NLRI_TYPE_LINK, &api.LsAttribute{Link: &api.LsAttributeLink{IgpMetric: metric}}))
		}
	}
	return out, nil
}

func lsPath(nlri *api.LsAddrPrefix_LsNLRI, typ api.LsNLRIType, attr *api.LsAttribute) *api.Path {
	return &api.Path{
		Family: lsFamily,
		Nlri: &api.NLRI{Nlri: &api.NLRI_LsAddrPrefix{LsAddrPrefix: &api.LsAddrPrefix{
			Type: typ, Nlri: nlri, ProtocolId: api.LsProtocolID_LS_PROTOCOL_ID_OSPF_V3,
		}}},
		Pattrs: []*api.Attribute{
			{Attr: &api.Attribute_Origin{Origin: &api.OriginAttribute{Origin: 0}}},
			{Attr: &api.Attribute_NextHop{NextHop: &api.NextHopAttribute{NextHop: "0.0.0.0"}}},
			{Attr: &api.Attribute_Ls{Ls: attr}},
		},
	}
}
