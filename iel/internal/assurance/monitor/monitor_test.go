package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// emptySource holds no SR Policies and reports them as nil.
type emptySource struct{}

func (emptySource) SRPolicies(context.Context) ([]domain.SRPolicy, error) { return nil, nil }

func TestObserveEmptySource(t *testing.T) {
	obs, err := Observe(context.Background(), emptySource{}, nil, nil, time.Second, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Empty, not nil: the headends were read and hold none (repair must not trust its cache).
	if obs.Policies == nil || len(obs.Policies) != 0 {
		t.Fatalf("Policies = %#v, want empty", obs.Policies)
	}
	obs, err = Observe(context.Background(), nil, nil, nil, time.Second, time.Now())
	if err != nil || obs.Policies != nil {
		t.Fatalf("no source: Policies = %#v, err = %v", obs.Policies, err)
	}
}
