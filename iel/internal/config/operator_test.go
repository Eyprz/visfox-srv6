package config

import (
	"context"
	"os"
	"testing"

	"github.com/eyprz/visfox-srv6/iel/internal/ssot/ted"
)

// The shipped operator config and TED seed must load and bind every IN / OUT.
func TestShippedOperatorConfigAndSeed(t *testing.T) {
	op, err := LoadOperator("../../config/operator.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := ted.SeedFile{Path: "../../db/ted.yaml"}.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := ted.Interpret(dev, op.SIDRule, op.Bindings())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Unclassified) != 0 || len(got.Sources) != 4 || len(got.Destinations) != 4 || len(got.Nodes) != 13 {
		t.Fatalf("unexpected TED: nodes=%d sources=%d destinations=%d unclassified=%v",
			len(got.Nodes), len(got.Sources), len(got.Destinations), got.Unclassified)
	}
}

// The lab network's operator config reads its TED from GoBGP, which carries what the stand-in BGP-LS
// speaker advertises from db/ted-lab.yaml: that file must bind every IN / OUT with the config's rule.
func TestShippedLabConfigAndTED(t *testing.T) {
	op, err := LoadOperator("../../config/operator-lab.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if op.TED.Source != TEDGoBGP || op.TED.GoBGP == "" {
		t.Fatalf("ted = %+v", op.TED)
	}
	dev, err := ted.SeedFile{Path: "../../db/ted-lab.yaml"}.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := ted.Interpret(dev, op.SIDRule, op.Bindings())
	if err != nil {
		t.Fatal(err)
	}
	// 4 headends, 2 switchers, 3 captions, 3 converters, 4 decaps; the core routers are transit.
	if len(got.Unclassified) != 2 || len(got.Sources) != 4 || len(got.Destinations) != 4 || len(got.Nodes) != 16 {
		t.Fatalf("unexpected TED: nodes=%d sources=%d destinations=%d unclassified=%v",
			len(got.Nodes), len(got.Sources), len(got.Destinations), got.Unclassified)
	}
}

func TestLoadOperatorTEDSource(t *testing.T) {
	for body, ok := range map[string]bool{
		"sid_rule: {name: d4s-testbed}\n":                                       true,
		"sid_rule: {name: d4s-testbed}\nted: {source: gobgp, gobgp: \"a:1\"}\n": true,
		"sid_rule: {name: d4s-testbed}\nted: {source: gobgp}\n":                 false,
		"sid_rule: {name: d4s-testbed}\nted: {source: bmp}\n":                   false,
	} {
		path := t.TempDir() + "/op.yaml"
		if err := writeFile(path, body); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOperator(path); (err == nil) != ok {
			t.Errorf("%q: err = %v", body, err)
		}
	}
}

func TestLoadOperatorRejectsUnknownFields(t *testing.T) {
	path := t.TempDir() + "/op.yaml"
	if err := writeFile(path, "sid_rule: {name: d4s-testbed}\nunknown: 1\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOperator(path); err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o600) }
