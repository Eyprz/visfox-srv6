package plan

import (
	"slices"
	"testing"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
)

var (
	selectVVF    = apitypes.VvfDefinitionBehaviorSelect
	replicateVVF = apitypes.VvfDefinitionBehaviorReplicate
	flowVVFs     = []apitypes.VvfDefinition{
		{Id: "vvf-switcher", Name: "Switcher", Behavior: &selectVVF},
		{Id: "vvf-replicator", Name: "Replicator", Behavior: &replicateVVF},
	}
)

func input(port, sid string, next int) domain.InputPort {
	return domain.InputPort{Port: port, Next: next, SID: sid}
}

// network is IN 1 / IN 2 and OUT 1 / OUT 2 with the given flow nodes, wiring and limits.
func network(t *testing.T, nodes []domain.Node, wiring []ssot.Wiring, limits ssot.Limits) Selector {
	t.Helper()
	ted := domain.TED{
		Nodes: append([]domain.Node{
			{ID: "h1", Hostname: "Encap-1", Role: domain.RoleHeadend},
			{ID: "h2", Hostname: "Encap-2", Role: domain.RoleHeadend},
			{ID: "decap", Hostname: "Decap", Role: domain.RoleDecap, SIDs: []string{"fd77:ff::"}},
		}, nodes...),
		Sources:      []domain.Endpoint{{ID: "in-1", NodeID: "h1"}, {ID: "in-2", NodeID: "h2"}},
		Destinations: []domain.Endpoint{{ID: "out-1", NodeID: "decap"}, {ID: "out-2", NodeID: "decap"}},
	}
	res, err := ssot.NewResources(ted, flowVVFs, wiring, limits)
	if err != nil {
		t.Fatal(err)
	}
	return Selector{Resources: res, SwitcherRequired: true}
}

func onAir(src, dst string) domain.SFCRequest {
	return domain.SFCRequest{IntentID: src + dst, SourceID: src, DestinationID: dst, OnAir: true}
}

func kinds(al Selection) []string {
	var out []string
	for _, f := range al.Failed {
		out = append(out, f.Pool)
	}
	return out
}

// A replicator (RFC 9524) lets one IN reach two OUTs with one stream from its headend: the headend's
// edge into the replicator is shared, the replicator uses one output per branch.
func TestReplicateSharesTheHeadendStream(t *testing.T) {
	nodes := []domain.Node{
		{ID: "rep", Hostname: "Replicator-1", Role: domain.RoleVVF, VVFID: "vvf-replicator", Inputs: []domain.InputPort{input("a", "fd77:1a::", 0)}},
		{ID: "sw1", Hostname: "Switcher-1", Role: domain.RoleVVF, VVFID: "vvf-switcher", Inputs: []domain.InputPort{input("a", "fd77:aa::", 0), input("b", "fd77:ab::", 1)}},
		{ID: "sw2", Hostname: "Switcher-2", Role: domain.RoleVVF, VVFID: "vvf-switcher", Inputs: []domain.InputPort{input("a", "fd77:da::", 0)}},
	}
	wiring := []ssot.Wiring{
		{Node: "Replicator-1", Inputs: map[string]string{"a": "in-1"}},
		{Node: "Switcher-1", Inputs: map[string]string{"a": "Replicator-1", "b": "in-2"}},
		{Node: "Switcher-2", Inputs: map[string]string{"a": "Replicator-1"}},
	}
	limits := ssot.Limits{SourceUnicasts: 1, Ports: map[string]ssot.Ports{
		"vvf-switcher": {Inputs: 2, Outputs: 1}, "vvf-replicator": {Inputs: 1, Outputs: 2},
	}}
	a := network(t, nodes, wiring, limits)
	al := a.Select([]domain.SFCRequest{onAir("in-1", "out-1"), onAir("in-1", "out-2")}, State{}, nil)
	if len(al.Failed) != 0 {
		t.Fatalf("failed = %+v", al.Failed)
	}
	if w1, w2 := al.SFPs["out-1"].Way, al.SFPs["out-2"].Way; w1[0] != (domain.Hop{Node: "rep", Port: "a"}) ||
		w2[0] != w1[0] || w1[1].Node == w2[1].Node {
		t.Fatalf("ways = %v / %v", w1, w2)
	}
	if got := al.Users[SourceKey("in-1")]; len(got) != 1 {
		t.Fatalf("headend streams = %v, want one shared", got)
	}

	// A replicator with one output cannot branch: its output is short, not the IN.
	limits.Ports["vvf-replicator"] = ssot.Ports{Inputs: 1, Outputs: 1}
	a = network(t, nodes, wiring, limits)
	al = a.Select([]domain.SFCRequest{onAir("in-1", "out-1"), onAir("in-1", "out-2")}, State{}, nil)
	if !slices.Equal(kinds(al), []string{ssot.PoolVVFOutput}) {
		t.Fatalf("failed = %+v", al.Failed)
	}
}

// Without a branch, one IN to two OUTs needs two streams from its headend (source_unicast).
func TestNoBranchNeedsTwoStreams(t *testing.T) {
	nodes := []domain.Node{
		{ID: "sw1", Hostname: "Switcher-1", Role: domain.RoleVVF, VVFID: "vvf-switcher", Inputs: []domain.InputPort{input("a", "fd77:aa::", 0)}},
		{ID: "sw2", Hostname: "Switcher-2", Role: domain.RoleVVF, VVFID: "vvf-switcher", Inputs: []domain.InputPort{input("a", "fd77:da::", 0)}},
	}
	wiring := []ssot.Wiring{
		{Node: "Switcher-1", Inputs: map[string]string{"a": "in-1"}},
		{Node: "Switcher-2", Inputs: map[string]string{"a": "in-1"}},
	}
	limits := ssot.Limits{SourceUnicasts: 1, Ports: map[string]ssot.Ports{"vvf-switcher": {Inputs: 3, Outputs: 1}}}
	al := network(t, nodes, wiring, limits).Select([]domain.SFCRequest{onAir("in-1", "out-1"), onAir("in-1", "out-2")}, State{}, nil)
	if !slices.Equal(kinds(al), []string{ssot.PoolSourceUnicast}) || al.Failed[0].DestinationID != "out-2" {
		t.Fatalf("failed = %+v", al.Failed)
	}
}

// A switcher with two outputs (a matrix) serves two OUTs: an input fed by an IN is shared, so one
// stream from the headend reaches both.
func TestMatrixSwitcherSharesItsInput(t *testing.T) {
	nodes := []domain.Node{
		{ID: "sw1", Hostname: "Switcher-1", Role: domain.RoleVVF, VVFID: "vvf-switcher", Inputs: []domain.InputPort{input("a", "fd77:aa::", 0), input("b", "fd77:ab::", 1)}},
	}
	wiring := []ssot.Wiring{{Node: "Switcher-1", Inputs: map[string]string{"a": "in-1", "b": "in-2"}}}
	limits := ssot.Limits{SourceUnicasts: 1, Ports: map[string]ssot.Ports{"vvf-switcher": {Inputs: 2, Outputs: 2}}}
	a := network(t, nodes, wiring, limits)
	al := a.Select([]domain.SFCRequest{onAir("in-1", "out-1"), onAir("in-1", "out-2")}, State{}, nil)
	if len(al.Failed) != 0 || len(al.Users[InputKey("sw1")]) != 1 || len(al.Users[SourceKey("in-1")]) != 1 {
		t.Fatalf("failed = %+v, users = %v", al.Failed, al.Users)
	}
}

// The stream a select node sends to another one is selected for one OUT: that input port is not
// shared, even when both select nodes have outputs left.
func TestSelectedStreamIsNotShared(t *testing.T) {
	nodes := []domain.Node{
		{ID: "sw1", Hostname: "Switcher-1", Role: domain.RoleVVF, VVFID: "vvf-switcher", Inputs: []domain.InputPort{input("e", "fd77:ae::", 0)}},
		{ID: "sw2", Hostname: "Switcher-2", Role: domain.RoleVVF, VVFID: "vvf-switcher", Inputs: []domain.InputPort{input("a", "fd77:da::", 0), input("b", "fd77:db::", 1)}},
	}
	wiring := []ssot.Wiring{
		{Node: "Switcher-1", Inputs: map[string]string{"e": "Switcher-2"}},
		{Node: "Switcher-2", Inputs: map[string]string{"a": "in-1", "b": "in-2"}},
	}
	limits := ssot.Limits{Ports: map[string]ssot.Ports{"vvf-switcher": {Outputs: 2}}}
	a := network(t, nodes, wiring, limits)
	// Both OUTs on air through SW2 -> SW1 (kept ways) would need SW1's input e twice.
	prev := State{Previous: map[string]domain.SFP{
		"out-1": {Way: []domain.Hop{{Node: "sw2", Port: "a"}, {Node: "sw1", Port: "e"}}},
		"out-2": {Way: []domain.Hop{{Node: "sw2", Port: "b"}, {Node: "sw1", Port: "e"}}},
	}}
	al := a.Select([]domain.SFCRequest{onAir("in-1", "out-1"), onAir("in-2", "out-2")}, prev, nil)
	if len(al.Failed) != 1 || al.Failed[0].DestinationID != "out-2" {
		t.Fatalf("failed = %+v", al.Failed)
	}
	// Taken again (not kept), OUT 2 goes through SW2 alone.
	al = a.Select([]domain.SFCRequest{onAir("in-1", "out-1"), onAir("in-2", "out-2")}, prev, map[string]bool{"out-2": true})
	if len(al.Failed) != 0 || len(al.SFPs["out-2"].Way) != 1 {
		t.Fatalf("failed = %+v, way = %v", al.Failed, al.SFPs["out-2"].Way)
	}
}

// Hot standby, testbed wiring (SW1: a-d from the INs, e from SW2; three inputs each). A Take only
// switches: the IN being taken stays on the way it is already fed on, even when a shorter way is
// free, so the switchers keep receiving the same streams on the same ports.
func TestTakeKeepsTheFedWay(t *testing.T) {
	nodes := []domain.Node{
		{ID: "sw1", Hostname: "Switcher-1", Role: domain.RoleVVF, VVFID: "vvf-switcher", Inputs: []domain.InputPort{
			input("a", "fd77:aa::", 0), input("b", "fd77:ab::", 1), input("c", "fd77:ac::", 2), input("d", "fd77:ad::", 3), input("e", "fd77:ae::", 4)}},
		{ID: "sw2", Hostname: "Switcher-2", Role: domain.RoleVVF, VVFID: "vvf-switcher", Inputs: []domain.InputPort{
			input("c", "fd77:dc::", 2), input("d", "fd77:dd::", 3)}},
	}
	wiring := []ssot.Wiring{
		{Node: "Switcher-1", Inputs: map[string]string{"a": "in-1", "b": "in-2", "c": "in-3", "d": "in-4", "e": "Switcher-2"}},
		{Node: "Switcher-2", Inputs: map[string]string{"c": "in-3", "d": "in-4"}},
	}
	limits := ssot.Limits{SourceUnicasts: 1, StandbyFeed: ssot.StandbyFeedSwitcher,
		Ports: map[string]ssot.Ports{"vvf-switcher": {Inputs: 3, Outputs: 1}}}
	ted := domain.TED{
		Nodes: append([]domain.Node{
			{ID: "h1", Role: domain.RoleHeadend}, {ID: "h2", Role: domain.RoleHeadend},
			{ID: "h3", Role: domain.RoleHeadend}, {ID: "h4", Role: domain.RoleHeadend},
			{ID: "decap", Role: domain.RoleDecap, SIDs: []string{"fd77:ff::"}},
		}, nodes...),
		Sources: []domain.Endpoint{{ID: "in-1", NodeID: "h1"}, {ID: "in-2", NodeID: "h2"},
			{ID: "in-3", NodeID: "h3"}, {ID: "in-4", NodeID: "h4"}},
		Destinations: []domain.Endpoint{{ID: "out-1", NodeID: "decap"}},
	}
	res, err := ssot.NewResources(ted, flowVVFs, wiring, limits)
	if err != nil {
		t.Fatal(err)
	}
	a := Selector{Resources: res, SwitcherRequired: true}
	feeds := map[string][]domain.Hop{
		"in-1": {{Node: "sw1", Port: "a"}},
		"in-2": {{Node: "sw1", Port: "b"}},
		"in-3": {{Node: "sw2", Port: "c"}, {Node: "sw1", Port: "e"}},
		"in-4": {{Node: "sw2", Port: "d"}, {Node: "sw1", Port: "e"}},
	}
	prev := State{Previous: map[string]domain.SFP{"out-1": {Way: feeds["in-1"], Feeds: feeds}}}

	// A on air -> Take D.
	take := domain.SFCRequest{IntentID: "d", SourceID: "in-4", DestinationID: "out-1", OnAir: true,
		Standby: []string{"in-1", "in-2", "in-3"}}
	al := a.Select([]domain.SFCRequest{take}, prev, map[string]bool{"out-1": true})
	if len(al.Failed) != 0 {
		t.Fatalf("failed = %+v", al.Failed)
	}
	got := al.SFPs["out-1"]
	if !slices.Equal(got.Way, feeds["in-4"]) {
		t.Fatalf("way of the taken IN = %v, want %v", got.Way, feeds["in-4"])
	}
	for src, w := range feeds {
		if !slices.Equal(got.Feeds[src], w) {
			t.Errorf("feed %s = %v, want %v", src, got.Feeds[src], w)
		}
	}
}
