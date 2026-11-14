package compliance

import (
	"testing"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

func TestAssess(t *testing.T) {
	a := domain.SRPolicy{Name: "visfox-in-1-out-1-decap", Headend: "h1", Endpoint: "decap", Color: 101, SegmentList: []string{"fd77:aa::", "fd77:ff::1"}}
	b := domain.SRPolicy{Name: "visfox-in-2-out-2-decap", Headend: "h2", Endpoint: "decap", Color: 102, SegmentList: []string{"fd77:ab::", "fd77:ff::2"}}
	moved := b
	moved.SegmentList = []string{"fd77:dd::", "fd77:ff::2"}
	stray := domain.SRPolicy{Name: "visfox-in-3-out-3-decap", Headend: "h3", Endpoint: "decap", Color: 103}
	foreign := domain.SRPolicy{Name: "operator-test", Headend: "h4", Endpoint: "decap", Color: 9}

	if d := Assess([]domain.SRPolicy{a, b}, []domain.SRPolicy{a, b, foreign}); !d.None() {
		t.Fatalf("drift = %v", d)
	}
	d := Assess([]domain.SRPolicy{a, b}, []domain.SRPolicy{moved, stray, foreign})
	if len(d.Missing) != 1 || d.Missing[0].Headend != "h1" || len(d.Different) != 1 || d.Different[0].SegmentList[0] != "fd77:ab::" ||
		len(d.Unexpected) != 1 || d.Unexpected[0].Headend != "h3" {
		t.Fatalf("drift = %+v", d)
	}
	if c := d.Colors(); len(c) != 3 || c[0] != 101 || c[2] != 103 {
		t.Fatalf("colors = %v", c)
	}
}
