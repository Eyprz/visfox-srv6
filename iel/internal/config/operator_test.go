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

func TestLoadOperatorPCEPath(t *testing.T) {
	const rule = "sid_rule: {name: d4s-testbed}\n"
	for body, want := range map[string]string{
		rule: "explicit/igp",
		rule + "orchestrator: {polapce: {path: dynamic}}\n":                "dynamic/igp",
		rule + "orchestrator: {polapce: {path: dynamic, metric: delay}}\n": "dynamic/delay",
		rule + "orchestrator: {polapce: {path: computed}}\n":               "",
		rule + "orchestrator: {polapce: {metric: fastest}}\n":              "",
	} {
		path := t.TempDir() + "/op.yaml"
		if err := writeFile(path, body); err != nil {
			t.Fatal(err)
		}
		o, err := LoadOperator(path)
		if want == "" {
			if err == nil {
				t.Errorf("%q: accepted", body)
			}
			continue
		}
		if got := o.Orchestrator.PolaPCE.Path + "/" + o.Orchestrator.PolaPCE.Metric; err != nil || got != want {
			t.Errorf("%q: %s, %v", body, got, err)
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

func TestOperatorDrivers(t *testing.T) {
	path := t.TempDir() + "/op.yaml"
	body := "sid_rule: {name: d4s-testbed}\norchestrator:\n  polapce: {address: \"a:1\"}\n  nodes:\n    SW: {mgmt: \"http://10.0.0.1:8080\"}\n"
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}
	op, err := LoadOperator(path)
	if err != nil {
		t.Fatal(err)
	}
	if op.Orchestrator.SRPolicy != DriverDryRun || op.Orchestrator.VVF != DriverDryRun {
		t.Fatalf("defaults = %+v", op.Orchestrator)
	}
	if err := op.Override(DriverPolaPCE, DriverMGMT, "", false); err != nil || op.Orchestrator.SRPolicy != DriverPolaPCE || op.Orchestrator.VVF != DriverMGMT {
		t.Fatalf("override = %+v, %v", op.Orchestrator, err)
	}
	// Dry run wins over every driver.
	if err := op.Override("", "", "", true); err != nil || op.Orchestrator.SRPolicy != DriverDryRun || op.Orchestrator.VVF != DriverDryRun {
		t.Fatalf("dry run = %+v, %v", op.Orchestrator, err)
	}
	if err := op.Override("", "d4s", "", false); err == nil {
		t.Fatal("unknown VVF driver accepted")
	}
	if err := op.Override("", "", PathDynamic, false); err != nil || op.Orchestrator.PolaPCE.Path != PathDynamic {
		t.Fatalf("pce path = %+v, %v", op.Orchestrator.PolaPCE, err)
	}
	if err := op.Override("", "", "computed", false); err == nil {
		t.Fatal("unknown PCE path accepted")
	}
	for _, bad := range []string{
		"sid_rule: {name: d4s-testbed}\norchestrator: {vvf: d4s}\n",
		"sid_rule: {name: d4s-testbed}\norchestrator:\n  nodes:\n    SW: {mgmt: \"10.0.0.1:8080\"}\n",
	} {
		if err := writeFile(path, bad); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOperator(path); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
