// Package compliance is the compliance assessment of Intent Assurance (RFC 9315 §5.2.2, analyze /
// validate): it compares what the SSoT says the network holds with what monitoring observed. The
// compliance action (repair: provision the SSoT again, LKG rollback) is run by the use cases through
// Intent Orchestration.
package compliance

import (
	"fmt"
	"slices"
	"strings"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// PolicyPrefix starts the name of every SR Policy the IEL installs; observed policies without it
// belong to someone else and are left alone.
const PolicyPrefix = "visfox-"

// Drift is how the observed network differs from the SSoT. Policies are told apart by headend,
// color and endpoint (RFC 9256).
type Drift struct {
	// Switchers are switcher VVFs that send on another input than the SSoT needs (Next is what they
	// send on).
	Switchers []domain.Selection
	// Missing are expected policies the network does not hold.
	Missing []domain.SRPolicy
	// Different are expected policies the network holds with another Segment List (the SSoT's).
	Different []domain.SRPolicy
	// Unexpected are IEL policies the network holds but the SSoT does not have.
	Unexpected []domain.SRPolicy
}

// None reports whether the network matches the SSoT.
func (d Drift) None() bool {
	return len(d.Missing)+len(d.Different)+len(d.Unexpected)+len(d.Switchers) == 0
}

// Colors are the colors (one per OUT) of the drifted policies.
func (d Drift) Colors() []uint32 {
	var out []uint32
	for _, list := range [][]domain.SRPolicy{d.Missing, d.Different, d.Unexpected} {
		for _, p := range list {
			if !slices.Contains(out, p.Color) {
				out = append(out, p.Color)
			}
		}
	}
	slices.Sort(out)
	return out
}

func (d Drift) String() string {
	out := fmt.Sprintf("%d missing, %d different, %d unexpected SR Policies", len(d.Missing), len(d.Different), len(d.Unexpected))
	for _, sw := range d.Switchers {
		out += fmt.Sprintf("; %s sends on input %c", sw.Node, rune('a'+sw.Next))
	}
	return out
}

// AssessSwitchers adds the switchers whose input differs from the SSoT's (expected) to the drift.
// Switchers that were not observed (unreadable, or no switcher source) are not judged.
func (d *Drift) AssessSwitchers(expected []domain.Selection, observed map[string]int) {
	for _, e := range expected {
		got, ok := observed[e.Node]
		if ok && got != e.Next {
			d.Switchers = append(d.Switchers, domain.Selection{DestinationID: e.DestinationID, Node: e.Node, Next: got})
		}
	}
}

func key(p domain.SRPolicy) string { return fmt.Sprintf("%s|%d|%s", p.Headend, p.Color, p.Endpoint) }

// Assess compares the expected SR Policies (SSoT) with the observed ones.
func Assess(expected, observed []domain.SRPolicy) Drift {
	var d Drift
	seen := map[string]domain.SRPolicy{}
	for _, o := range observed {
		seen[key(o)] = o
	}
	want := map[string]bool{}
	for _, e := range expected {
		want[key(e)] = true
		o, ok := seen[key(e)]
		switch {
		case !ok:
			d.Missing = append(d.Missing, e)
		case !slices.Equal(o.SegmentList, e.SegmentList):
			d.Different = append(d.Different, e)
		}
	}
	for _, o := range observed {
		if !want[key(o)] && strings.HasPrefix(o.Name, PolicyPrefix) {
			d.Unexpected = append(d.Unexpected, o)
		}
	}
	return d
}
