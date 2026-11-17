// Package monitor is the monitoring part of Intent Assurance (RFC 9315 §5.2.1, monitor / observe):
// it collects what the network reports through the SBI clients: the SR Policies the headends hold
// (PolaPCE, their PCEP state reports) and the inputs the switcher VVFs send on (VVF control). VVF
// configs (VNF Manager) and telemetry (In-band OAM) join later.
package monitor

import (
	"context"
	"time"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// SRPolicySource reports the SR Policies the headends hold (internal/sbi/pce Client).
type SRPolicySource interface {
	SRPolicies(ctx context.Context) ([]domain.SRPolicy, error)
}

// SwitcherSource reports the input a switcher VVF sends on (internal/sbi/vvf Client).
type SwitcherSource interface {
	Selected(ctx context.Context, nodeID string) (int, error)
}

// Observation is what the network reported at one time.
type Observation struct {
	At time.Time
	// Policies are the SR Policies; nil when there is no SR Policy source.
	Policies []domain.SRPolicy
	// Selections are the inputs the switchers send on (node id -> next); Unread are the switchers
	// that could not be read, with the reason.
	Selections map[string]int
	Unread     map[string]error
}

// Observe collects an observation: the SR Policies from src (nil: none) and the inputs of the
// switchers from sw (nil: none), each read bounded by timeout (0: by ctx only). An unreadable
// switcher does not fail the observation.
func Observe(ctx context.Context, src SRPolicySource, sw SwitcherSource, switchers []string, timeout time.Duration, now time.Time) (Observation, error) {
	bound := func() (context.Context, context.CancelFunc) {
		if timeout <= 0 {
			return ctx, func() {}
		}
		return context.WithTimeout(ctx, timeout)
	}
	obs := Observation{At: now, Selections: map[string]int{}, Unread: map[string]error{}}
	if src != nil {
		rctx, cancel := bound()
		pols, err := src.SRPolicies(rctx)
		cancel()
		if err != nil {
			return Observation{}, err
		}
		obs.Policies = pols
	}
	if sw != nil {
		for _, id := range switchers {
			rctx, cancel := bound()
			next, err := sw.Selected(rctx, id)
			cancel()
			if err != nil {
				obs.Unread[id] = err
				continue
			}
			obs.Selections[id] = next
		}
	}
	return obs, nil
}
