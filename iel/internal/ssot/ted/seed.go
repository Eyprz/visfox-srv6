package ted

import (
	"context"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/eyprz/visfox-srv6/iel/internal/domain/tedyang"
)

// NetworkID is the network id of the SRv6 SFC domain TED in the YANG instance.
const NetworkID = "visfox-srv6-sfc"

// Source loads the TED facts (nodes and SIDs) as a validated YANG instance (ietf-network +
// visfox-srv6-ted). v1 reads a static seed; v2 adds a BGP-LS source without changing the rest.
type Source interface {
	Load(ctx context.Context) (*tedyang.Device, error)
}

// Seed is a static stand-in for the BGP-LS advertisement: node names, router ids and SIDs. The local
// C-Plane lab's stand-in BGP-LS speaker (infra/cplane/fakebgpls) advertises the same format into
// GoBGP, with the loopbacks and link metrics PolaPCE needs for path computation.
type Seed struct {
	// SIDStructure is the structure of the SIDs that do not give their own.
	SIDStructure *SeedSIDShape `yaml:"sid_structure"`
	Nodes        []SeedNode    `yaml:"nodes"`
	Links        []SeedLink    `yaml:"links"`
}

// SeedNode is one advertised node.
type SeedNode struct {
	Name     string `yaml:"name"`
	RouterID string `yaml:"router_id"`
	// Loopback is the node's IPv6 loopback (BGP-LS prefix); PolaPCE resolves policy headends and
	// endpoints with it. The IEL does not read it.
	Loopback string    `yaml:"loopback"`
	SIDs     []SeedSID `yaml:"sids"`
}

// SeedSID is one advertised SID (without the argument).
type SeedSID struct {
	Value     string        `yaml:"value"`
	Behavior  string        `yaml:"behavior"`
	Structure *SeedSIDShape `yaml:"structure"`
}

// SeedSIDShape is the advertised SID structure (RFC 9514), all lengths in bits.
type SeedSIDShape struct {
	LocatorBlock uint8 `yaml:"locator_block"`
	LocatorNode  uint8 `yaml:"locator_node"`
	Function     uint8 `yaml:"function"`
	Argument     uint8 `yaml:"argument"`
}

// SeedLink is one advertised link between two nodes (both directions).
type SeedLink struct {
	Source      string `yaml:"source"`
	Destination string `yaml:"destination"`
	// Metric is the IGP metric PolaPCE computes paths with (1 when zero). The IEL does not read it.
	Metric uint32 `yaml:"metric"`
}

// UnmarshalYAML accepts a bare string as a SID value.
func (s *SeedSID) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		s.Value = n.Value
		return nil
	}
	type plain SeedSID
	return n.Decode((*plain)(s))
}

// SeedFile reads the seed from a YAML file.
type SeedFile struct {
	Path string
}

// Load implements Source.
func (f SeedFile) Load(_ context.Context) (*tedyang.Device, error) {
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, fmt.Errorf("read TED seed: %w", err)
	}
	var seed Seed
	if err := yaml.Unmarshal(raw, &seed); err != nil {
		return nil, fmt.Errorf("parse TED seed %s: %w", f.Path, err)
	}
	return seed.ToYANG()
}

// ToYANG converts the seed into a validated YANG instance.
func (s Seed) ToYANG() (*tedyang.Device, error) {
	dev := &tedyang.Device{Networks: &tedyang.IETFNetwork_Networks{}}
	nw, err := dev.Networks.NewNetwork(NetworkID)
	if err != nil {
		return nil, err
	}
	nw.NetworkTypes = &tedyang.IETFNetwork_Networks_Network_NetworkTypes{
		Srv6Sfc: &tedyang.IETFNetwork_Networks_Network_NetworkTypes_Srv6Sfc{},
	}

	for _, sn := range s.Nodes {
		node, err := nw.NewNode(sn.Name)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", sn.Name, err)
		}
		srv6 := &tedyang.IETFNetwork_Networks_Network_Node_Srv6{Hostname: ptr(sn.Name)}
		if sn.RouterID != "" {
			srv6.RouterId = ptr(sn.RouterID)
		}
		for _, ss := range sn.SIDs {
			sid, err := srv6.NewSid(ss.Value)
			if err != nil {
				return nil, fmt.Errorf("node %q SID %q: %w", sn.Name, ss.Value, err)
			}
			if ss.Behavior != "" {
				sid.Behavior = ptr(ss.Behavior)
			}
			st := ss.Structure
			if st == nil {
				st = s.SIDStructure
			}
			if st != nil {
				sid.Structure = &tedyang.IETFNetwork_Networks_Network_Node_Srv6_Sid_Structure{
					LocatorBlockLength: ptr(st.LocatorBlock),
					LocatorNodeLength:  ptr(st.LocatorNode),
					FunctionLength:     ptr(st.Function),
					ArgumentLength:     ptr(st.Argument),
				}
			}
		}
		node.Srv6 = srv6
	}

	for _, sl := range s.Links {
		link, err := nw.NewLink(sl.Source + "--" + sl.Destination)
		if err != nil {
			return nil, err
		}
		link.Source = &tedyang.IETFNetwork_Networks_Network_Link_Source{SourceNode: ptr(sl.Source)}
		link.Destination = &tedyang.IETFNetwork_Networks_Network_Link_Destination{DestNode: ptr(sl.Destination)}
	}

	if err := dev.Validate(); err != nil {
		return nil, fmt.Errorf("TED does not conform to the YANG model: %w", err)
	}
	return dev, nil
}

func ptr[T any](v T) *T { return &v }
