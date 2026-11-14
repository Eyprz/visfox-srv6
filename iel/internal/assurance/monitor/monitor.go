// Package monitor is the monitoring part of Intent Assurance (RFC 9315 §5.2.1, monitor / observe):
// it collects what the network reports through the SBI clients. v1 reads the SR Policies the
// headends hold from PolaPCE (their PCEP state reports); switchers, VVF nodes (VNF Manager) and
// telemetry (In-band OAM) join later.
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

// Observation is what the network reported at one time.
type Observation struct {
	At       time.Time
	Policies []domain.SRPolicy
}

// Observe collects an observation.
func Observe(ctx context.Context, src SRPolicySource, now time.Time) (Observation, error) {
	pols, err := src.SRPolicies(ctx)
	if err != nil {
		return Observation{}, err
	}
	return Observation{At: now, Policies: pols}, nil
}
