package config

import (
	"bytes"
	"fmt"
	"net/netip"
	"net/url"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot/ted"
)

// Operator is the network operator config: settings about the network that the video operators
// using the WebUI never change. It is read once at startup.
type Operator struct {
	// TED is where the TED comes from.
	TED TEDSource `yaml:"ted"`
	// SIDRule is the SID interpretation rule shared with the lower layer.
	SIDRule      ted.Rule      `yaml:"sid_rule"`
	Sources      []ted.Binding `yaml:"sources"`
	Destinations []ted.Binding `yaml:"destinations"`
	// Labels are display names for some nodes (host name -> name), e.g. pinned caption materials.
	Labels map[string]string `yaml:"labels"`
	// Wiring is the physical wiring of the input ports of the VVF nodes that control the stream flow
	// (switchers; testbed constraint).
	Wiring []ssot.Wiring `yaml:"wiring"`
	// Limits are the resource pool capacities the TED does not advertise.
	Limits ssot.Limits `yaml:"limits"`
	// Orchestrator selects the southbound drivers and how they reach the nodes.
	Orchestrator Orchestrator `yaml:"orchestrator"`
}

// TED sources (TEDSource.Source).
const (
	// TEDSeed is the static TED file (-ted-seed / VISFOX_TED_SEED).
	TEDSeed = "seed"
	// TEDGoBGP is the BGP-LS RIB of a GoBGP collector (internal/sbi/bgpls).
	TEDGoBGP = "gobgp"
)

// TEDSource selects the data source of the TED pipeline.
type TEDSource struct {
	// Source is TEDSeed (default) or TEDGoBGP.
	Source string `yaml:"source"`
	// GoBGP is the collector's gRPC API (host:port) for TEDGoBGP.
	GoBGP string `yaml:"gobgp"`
}

// Southbound drivers: SR Policies (Orchestrator.SRPolicy) and VVF control (Orchestrator.VVF).
const (
	// DriverDryRun logs the instructions instead of sending them (health checks, validation, demos).
	DriverDryRun = "dryrun"
	// DriverPolaPCE sends SR Policies to PolaPCE (gRPC -> PCEP -> headends).
	DriverPolaPCE = "polapce"
	// DriverMGMT controls the VVFs over their routers' MGMT REST API (internal/sbi/vvf).
	DriverMGMT = "mgmt"
)

// Orchestrator is the southbound part of the network operator config.
type Orchestrator struct {
	// SRPolicy is the driver of the SR Policy instructions: "dryrun" (default, log only) or
	// "polapce".
	SRPolicy string `yaml:"sr_policy"`
	// VVF is the driver of the VVF control instructions (switcher input selection, VVF configs):
	// "dryrun" (default, log only) or "mgmt" (the routers' MGMT REST API, nodes.<host>.mgmt).
	VVF     string `yaml:"vvf"`
	PolaPCE struct {
		// Address is polad's gRPC server (host:port).
		Address string `yaml:"address"`
		// ASN is passed with every request (Pola's TED is per AS).
		ASN uint32 `yaml:"asn"`
		// SIDValidate makes Pola check the SIDs against its TED (BGP-LS). Leave it off while the
		// IEL's TED is static (v1): Pola's TED does not know those SIDs.
		SIDValidate bool `yaml:"sid_validate"`
	} `yaml:"polapce"`
	// Nodes maps advertised host names to their addresses: the PCEP session (PCC) and SRv6 address
	// of every headend, and the SRv6 address of every policy endpoint (decap; switchers in swap
	// mode). v2 reads them from BGP-LS.
	Nodes map[string]NodeAddress `yaml:"nodes"`
	// ColorBase + the OUT's position (1-based) is the SR Policy color of streams to that OUT.
	ColorBase uint32 `yaml:"color_base"`
	// VVFSettleMs is waited after the VVF configs of a Take, before the switch.
	VVFSettleMs int `yaml:"vvf_settle_ms"`
}

// NodeAddress is a node's PCEP session address (headends only), SRv6 address, and the base URL of
// the MGMT REST API of its router (VVF nodes, for orchestrator.vvf: mgmt).
type NodeAddress struct {
	PCEPPeer string `yaml:"pcep_peer"`
	Address  string `yaml:"address"`
	MGMT     string `yaml:"mgmt"`
}

// Bindings returns the IN / OUT bindings.
func (o Operator) Bindings() ted.Bindings {
	return ted.Bindings{Sources: o.Sources, Destinations: o.Destinations, Labels: o.Labels}
}

// LoadOperator reads the network operator config from a YAML file (Override applies the CLI /
// environment overrides of the drivers).
func LoadOperator(path string) (Operator, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Operator{}, fmt.Errorf("read operator config: %w", err)
	}
	var o Operator
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&o); err != nil {
		return Operator{}, fmt.Errorf("parse operator config %s: %w", path, err)
	}
	switch o.TED.Source {
	case "":
		o.TED.Source = TEDSeed
	case TEDSeed:
	case TEDGoBGP:
		if o.TED.GoBGP == "" {
			return Operator{}, fmt.Errorf("operator config %s: ted.gobgp is required with ted.source gobgp", path)
		}
	default:
		return Operator{}, fmt.Errorf("operator config %s: unknown ted.source %q", path, o.TED.Source)
	}
	switch o.Orchestrator.SRPolicy {
	case "":
		o.Orchestrator.SRPolicy = DriverDryRun
	case DriverDryRun:
	case DriverPolaPCE:
		if o.Orchestrator.PolaPCE.Address == "" {
			return Operator{}, fmt.Errorf("operator config %s: orchestrator.polapce.address is required", path)
		}
	default:
		return Operator{}, fmt.Errorf("operator config %s: unknown orchestrator.sr_policy %q", path, o.Orchestrator.SRPolicy)
	}
	switch o.Orchestrator.VVF {
	case "":
		o.Orchestrator.VVF = DriverDryRun
	case DriverDryRun, DriverMGMT:
	default:
		return Operator{}, fmt.Errorf("operator config %s: unknown orchestrator.vvf %q", path, o.Orchestrator.VVF)
	}
	for host, a := range o.Orchestrator.Nodes {
		for _, s := range []string{a.PCEPPeer, a.Address} {
			if s == "" {
				continue
			}
			if _, err := netip.ParseAddr(s); err != nil {
				return Operator{}, fmt.Errorf("operator config %s: orchestrator.nodes.%s: %w", path, host, err)
			}
		}
		if a.MGMT != "" {
			if u, err := url.Parse(a.MGMT); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return Operator{}, fmt.Errorf("operator config %s: orchestrator.nodes.%s.mgmt %q is not an http(s) URL", path, host, a.MGMT)
			}
		}
	}
	return o, nil
}

// Override applies the CLI / environment overrides of the southbound drivers: srPolicy and vvf
// replace the config's drivers when set; dryRun sends everything to the dry-run driver.
func (o *Operator) Override(srPolicy, vvf string, dryRun bool) error {
	if srPolicy != "" {
		if srPolicy != DriverDryRun && srPolicy != DriverPolaPCE {
			return fmt.Errorf("unknown SR Policy driver %q", srPolicy)
		}
		if srPolicy == DriverPolaPCE && o.Orchestrator.PolaPCE.Address == "" {
			return fmt.Errorf("orchestrator.polapce.address is required for the polapce driver")
		}
		o.Orchestrator.SRPolicy = srPolicy
	}
	if vvf != "" {
		if vvf != DriverDryRun && vvf != DriverMGMT {
			return fmt.Errorf("unknown VVF driver %q", vvf)
		}
		o.Orchestrator.VVF = vvf
	}
	if dryRun {
		o.Orchestrator.SRPolicy, o.Orchestrator.VVF = DriverDryRun, DriverDryRun
	}
	return nil
}
