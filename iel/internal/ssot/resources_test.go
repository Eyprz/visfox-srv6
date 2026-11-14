package ssot

import (
	"strings"
	"testing"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

func testTED() domain.TED {
	return domain.TED{
		Nodes: []domain.Node{
			{ID: "headend-a", Role: domain.RoleHeadend, Hostname: "Encap-A"},
			{ID: "sw-fd77-a", Label: "SW1", Role: domain.RoleVVF, VVFID: "vvf-switcher", Hostname: "Switcher-1", Inputs: []domain.InputPort{
				{Port: "a", Next: 0, SID: "fd77:aa::"}, {Port: "b", Next: 1, SID: "fd77:ab::"},
			}},
			{ID: "sw-fd77-d", Label: "SW2", Role: domain.RoleVVF, VVFID: "vvf-switcher", Hostname: "Switcher-2", Inputs: []domain.InputPort{
				{Port: "a", Next: 0, SID: "fd77:da::"},
			}},
			{ID: "caption-fd77-ba", Label: "Caption #1", Role: domain.RoleVVF, VVFID: "vvf-caption", Hostname: "Caption-1"},
		},
		Sources:      []domain.Endpoint{{ID: "in-1", Name: "A", VideoFormat: "8k_dg", NodeID: "headend-a"}},
		Destinations: []domain.Endpoint{{ID: "out-1", Name: "Z", VideoFormat: "8k_full"}},
	}
}

// testVVFs are a caption VVF and a switcher (select) VVF.
func testVVFs() []apitypes.VvfDefinition {
	sel := apitypes.VvfDefinitionBehaviorSelect
	return []apitypes.VvfDefinition{
		{Id: "vvf-caption", Name: "Caption"},
		{Id: "vvf-switcher", Name: "Switcher", Behavior: &sel},
	}
}

func TestNewResourcesWiring(t *testing.T) {
	r, err := NewResources(testTED(), testVVFs(), []Wiring{
		{Node: "Switcher-1", Inputs: map[string]string{"a": "in-1", "b": "Switcher-2"}},
	}, Limits{VVFStreams: map[string]int{"vvf-caption": 3}, Ports: map[string]Ports{"vvf-switcher": {Outputs: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	sw1 := r.Switchers[0]
	if len(sw1.Inputs) != 2 || sw1.Inputs[1].From != "sw-fd77-d" || sw1.DestinationIds[0] != "out-1" {
		t.Fatalf("SW1 = %+v", sw1)
	}
	if len(r.Switchers[1].Inputs) != 0 {
		t.Fatalf("unwired SW2 offers inputs: %+v", r.Switchers[1])
	}
	pools := r.Pools(func(string, string) []apitypes.PoolUse { return nil })
	byID := map[string]int{}
	for _, p := range pools {
		if p.Capacity != nil {
			byID[p.Id] = *p.Capacity
		} else {
			byID[p.Id] = -1
		}
	}
	if byID["vvf_node:caption-fd77-ba"] != 3 || byID["vvf_output:sw-fd77-a"] != 1 || byID["vvf_input:sw-fd77-a"] != -1 {
		t.Fatalf("pools = %v", byID)
	}
}

func TestNewResourcesRejectsBadWiring(t *testing.T) {
	cases := map[string]Wiring{
		"not a switcher": {Node: "Caption-1", Inputs: map[string]string{"a": "in-1"}},
		"unknown from":   {Node: "Switcher-1", Inputs: map[string]string{"a": "in-9"}},
		"self":           {Node: "Switcher-1", Inputs: map[string]string{"a": "Switcher-1"}},
		"no input SID":   {Node: "Switcher-2", Inputs: map[string]string{"c": "in-1"}},
	}
	for name, w := range cases {
		if _, err := NewResources(testTED(), testVVFs(), []Wiring{w}, Limits{}); err == nil || !strings.Contains(err.Error(), "wiring") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
