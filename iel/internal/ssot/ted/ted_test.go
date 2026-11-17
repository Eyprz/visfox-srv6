package ted

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/eyprz/visfox-srv6/iel/db"
	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/domain/tedyang"
)

var testRule = Rule{
	Name: RuleD4STestbed,
	Roles: []RoleRule{
		{Match: "encap", Role: domain.RoleHeadend, IDPrefix: "headend", Label: "Headend {n}"},
		{Match: "switcher", Role: domain.RoleVVF, VVFID: "vvf-switcher", InputSIDs: true, IDPrefix: "sw", Label: "SW{n}"},
		{Match: "caption", Role: domain.RoleVVF, VVFID: "vvf-caption", IDPrefix: "caption", Label: "Caption #{n}"},
		{Match: "decap", Role: domain.RoleDecap, IDPrefix: "decap", Label: "Decap {n}"},
	},
}

func testSeed() Seed {
	return Seed{Nodes: []SeedNode{
		{Name: "Encap-A", RouterID: "10.0.0.1"},
		{Name: "Switcher-1", SIDs: []SeedSID{{Value: "fd77:ad::"}, {Value: "fd77:aa::"}, {Value: "fd77:ab::"}}},
		{Name: "Caption-2", SIDs: []SeedSID{{Value: "fd77:bb::", Behavior: "End.AN"}}},
		{Name: "Caption-1", SIDs: []SeedSID{{Value: "fd77:ba::"}}},
		{Name: "Decap", SIDs: []SeedSID{{Value: "fd77:ff::"}}},
		{Name: "Mystery", SIDs: []SeedSID{{Value: "fd77:ee::"}}},
	}}
}

func interpretSeed(t *testing.T, s Seed, b Bindings) domain.TED {
	t.Helper()
	dev, err := s.ToYANG()
	if err != nil {
		t.Fatal(err)
	}
	ted, err := Interpret(dev, testRule, b)
	if err != nil {
		t.Fatal(err)
	}
	return ted
}

func nodeByID(t *testing.T, ted domain.TED, id string) domain.Node {
	t.Helper()
	i := slices.IndexFunc(ted.Nodes, func(n domain.Node) bool { return n.ID == id })
	if i < 0 {
		t.Fatalf("node %s not found in %v", id, ted.Nodes)
	}
	return ted.Nodes[i]
}

func TestToYANGRejectsNonIPv6SID(t *testing.T) {
	s := Seed{Nodes: []SeedNode{{Name: "Caption-1", SIDs: []SeedSID{{Value: "not-a-sid"}}}}}
	if _, err := s.ToYANG(); err == nil || !strings.Contains(err.Error(), "YANG") {
		t.Fatalf("expected a YANG validation error, got %v", err)
	}
}

func TestInterpretDerivesRolesIDsAndLabels(t *testing.T) {
	ted := interpretSeed(t, testSeed(), Bindings{
		Sources: []Binding{{ID: "in-1", Name: "IN 1", VideoFormat: "8k_dg", Node: "Encap-A"}},
	})

	if got := nodeByID(t, ted, "headend-encap-a"); got.Role != domain.RoleHeadend || got.RouterID != "10.0.0.1" {
		t.Errorf("headend: %+v", got)
	}
	// Labels follow the derived ids, not the advertisement order.
	if got := nodeByID(t, ted, "caption-fd77-ba"); got.Label != "Caption #1" || got.VVFID != "vvf-caption" {
		t.Errorf("caption-1: %+v", got)
	}
	if got := nodeByID(t, ted, "caption-fd77-bb"); got.Label != "Caption #2" {
		t.Errorf("caption-2: %+v", got)
	}
	if got := nodeByID(t, ted, "decap-fd77-ff"); got.Role != domain.RoleDecap {
		t.Errorf("decap: %+v", got)
	}
	if !slices.Equal(ted.Unclassified, []string{"Mystery"}) {
		t.Errorf("unclassified = %v", ted.Unclassified)
	}
	if len(ted.Sources) != 1 || ted.Sources[0].NodeID != "headend-encap-a" {
		t.Errorf("sources = %+v", ted.Sources)
	}
}

func TestInterpretLabelsByHostNameOrRouterID(t *testing.T) {
	s := testSeed()
	s.Nodes[2].RouterID = "10.0.2.2" // Caption-2
	ted := interpretSeed(t, s, Bindings{Labels: map[string]string{
		"10.0.2.2":  "Caption B",
		"Caption-1": "Caption A",
	}})
	if got := nodeByID(t, ted, "caption-fd77-bb").Label; got != "Caption B" {
		t.Errorf("label by router id = %q", got)
	}
	if got := nodeByID(t, ted, "caption-fd77-ba").Label; got != "Caption A" {
		t.Errorf("label by host name = %q", got)
	}
}

func TestInterpretSwitcherInputsUseSIDRank(t *testing.T) {
	sw := nodeByID(t, interpretSeed(t, testSeed(), Bindings{}), "sw-fd77-a")
	want := []domain.InputPort{
		{Port: "a", Next: 0, SID: "fd77:aa::"},
		{Port: "b", Next: 1, SID: "fd77:ab::"},
		// fd77:ac:: is missing: the gap collapses, so ad is the third input.
		{Port: "c", Next: 2, SID: "fd77:ad::"},
	}
	if !slices.Equal(sw.Inputs, want) {
		t.Fatalf("inputs = %+v", sw.Inputs)
	}
}

func TestInterpretIsDeterministic(t *testing.T) {
	a := interpretSeed(t, testSeed(), Bindings{})
	s := testSeed()
	slices.Reverse(s.Nodes)
	b := interpretSeed(t, s, Bindings{})
	ids := func(t domain.TED) []string {
		var out []string
		for _, n := range t.Nodes {
			out = append(out, n.ID+"="+n.Label)
		}
		return out
	}
	if !slices.Equal(ids(a), ids(b)) {
		t.Fatalf("ids depend on the advertisement order:\n%v\n%v", ids(a), ids(b))
	}
}

func TestInterpretErrors(t *testing.T) {
	dup := testSeed()
	dup.Nodes = append(dup.Nodes, SeedNode{Name: "Caption-9", SIDs: []SeedSID{{Value: "fd77:ba::"}}})

	cases := map[string]struct {
		seed Seed
		rule Rule
		b    Bindings
		want string
	}{
		"unknown rule":  {testSeed(), Rule{Name: "other"}, Bindings{}, "unknown SID interpretation rule"},
		"duplicate SID": {dup, testRule, Bindings{}, "advertised by both"},
		"missing node":  {testSeed(), testRule, Bindings{Destinations: []Binding{{ID: "out-1", Node: "Nope"}}}, "not in the TED"},
		"unknown label": {testSeed(), testRule, Bindings{Labels: map[string]string{"10.9.9.9": "x"}}, "no such host name or router id"},
		"label twice":   {testSeed(), testRule, Bindings{Labels: map[string]string{"Encap-A": "x", "10.0.0.1": "y"}}, "twice"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dev, err := tc.seed.ToYANG()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Interpret(dev, tc.rule, tc.b); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	store, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "t.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Exec(db.Schema); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestBuildPersistsAndReplaces(t *testing.T) {
	ctx := context.Background()
	store := openTestDB(t)
	b := Bindings{Destinations: []Binding{{ID: "out-1", Name: "OUT 1", VideoFormat: "8k_full", Node: "Decap"}}}

	for range 2 { // a rebuild replaces the previous TED
		if _, err := Build(ctx, staticSource{testSeed()}, testRule, b, store); err != nil {
			t.Fatal(err)
		}
	}

	var nodes, inputs int
	if err := store.QueryRow(`SELECT COUNT(*) FROM ted_nodes`).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM ted_sids WHERE node_id = 'sw-fd77-a' AND port IS NOT NULL`).Scan(&inputs); err != nil {
		t.Fatal(err)
	}
	var dest string
	if err := store.QueryRow(`SELECT node_id FROM destinations WHERE id = 'out-1'`).Scan(&dest); err != nil {
		t.Fatal(err)
	}
	if nodes != 5 || inputs != 3 || dest != "decap-fd77-ff" {
		t.Fatalf("nodes=%d inputs=%d dest=%s", nodes, inputs, dest)
	}
}

type staticSource struct{ seed Seed }

func (s staticSource) Load(context.Context) (*tedyang.Device, error) { return s.seed.ToYANG() }

// The lab network's TED (db/ted-lab.yaml) read with the rfc8986 rule of its operator config: End
// SIDs are node SIDs (not services), ids come from the locator, core routers stay unclassified.
func TestInterpretRFC8986Lab(t *testing.T) {
	dev, err := SeedFile{Path: "../../../db/ted-lab.yaml"}.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rule := testRule
	rule.Name = RuleRFC8986
	rule.Roles = append(slices.Clone(rule.Roles), RoleRule{Match: "converter", Role: domain.RoleVVF, IDPrefix: "ctc"})
	ted, err := Interpret(dev, rule, Bindings{
		Sources:      []Binding{{ID: "in-1", Node: "100G-8KDG-Encap-A"}},
		Destinations: []Binding{{ID: "out-1", Node: "100G-8KDG-Decap-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sw := nodeByID(t, ted, "sw-21")
	if !slices.Equal(sw.SIDs, []string{"fd00:0:21:a::", "fd00:0:21:b::", "fd00:0:21:c::", "fd00:0:21:d::", "fd00:0:21:e::"}) ||
		len(sw.Inputs) != 5 || sw.Inputs[0] != (domain.InputPort{Port: "a", Next: 0, SID: "fd00:0:21:a::"}) {
		t.Fatalf("switcher = %+v", sw)
	}
	if c := nodeByID(t, ted, "caption-31"); !slices.Equal(c.SIDs, []string{"fd00:0:31:a::"}) || c.RouterID != "10.255.2.1" {
		t.Fatalf("caption = %+v", c)
	}
	if d := nodeByID(t, ted, "decap-51"); !slices.Equal(d.SIDs, []string{"fd00:0:51::"}) {
		t.Fatalf("decap = %+v", d)
	}
	if h := nodeByID(t, ted, "headend-100g-8kdg-encap-a"); len(h.SIDs) != 0 {
		t.Fatalf("headend = %+v", h)
	}
	if !slices.Equal(ted.Unclassified, []string{"Core-P-1", "Core-P-2"}) {
		t.Fatalf("unclassified = %v", ted.Unclassified)
	}
	if ted.Destinations[0].NodeID != "decap-51" {
		t.Fatalf("destinations = %+v", ted.Destinations)
	}
}

func TestInterpretRFC8986NeedsStructure(t *testing.T) {
	dev, err := Seed{Nodes: []SeedNode{{Name: "Caption-1", SIDs: []SeedSID{{Value: "fd00:0:31:a::"}}}}}.ToYANG()
	if err != nil {
		t.Fatal(err)
	}
	rule := testRule
	rule.Name = RuleRFC8986
	if _, err := Interpret(dev, rule, Bindings{}); err == nil || !strings.Contains(err.Error(), "structure") {
		t.Fatalf("err = %v", err)
	}
}
