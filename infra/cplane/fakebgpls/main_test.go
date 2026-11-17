package main

import (
	"slices"
	"testing"
)

func TestNodeSIDsOnly(t *testing.T) {
	in := TED{Nodes: []Node{
		{Name: "SW", SIDs: []SID{{Value: "fd00:0:21:a::", Behavior: "End.AN"}, {Value: "fd00:0:21::", Behavior: "End"}}},
		{Name: "Decap", SIDs: []SID{{Value: "fd00:0:51::", Behavior: "End.DT6"}}},
	}}
	out := NodeSIDsOnly(in)
	values := func(n Node) []string {
		var v []string
		for _, s := range n.SIDs {
			v = append(v, s.Value)
		}
		return v
	}
	if got := values(out.Nodes[0]); !slices.Equal(got, []string{"fd00:0:21::"}) {
		t.Errorf("switcher = %v", got)
	}
	if got := values(out.Nodes[1]); !slices.Equal(got, []string{"fd00:0:51::"}) {
		t.Errorf("decap = %v", got)
	}
	if len(in.Nodes[0].SIDs) != 2 {
		t.Error("the input TED changed")
	}
}
