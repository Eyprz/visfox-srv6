package provision

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

type stepDriver struct {
	steps *[]string
	err   error
	block bool
}

func (stepDriver) Name() string { return "step" }

func (d stepDriver) Apply(ctx context.Context, in domain.Instruction) error {
	if d.block {
		<-ctx.Done()
		return ctx.Err()
	}
	*d.steps = append(*d.steps, string(in.Op))
	return d.err
}

func TestProvisionOrderSettleAndFailure(t *testing.T) {
	var steps []string
	ok := stepDriver{steps: &steps}
	o := New(Options{Settle: time.Second, Drivers: map[domain.Op]Driver{
		domain.OpFunctionSetParams: ok, domain.OpSRPolicySet: ok,
		domain.OpSwitcherSelect: stepDriver{steps: &steps, err: errors.New("busy")},
	}})
	o.sleep = func(context.Context, time.Duration) error { steps = append(steps, "settle"); return nil }
	err := o.Provision(context.Background(), []domain.Instruction{
		{Op: domain.OpFunctionSetParams}, {Op: domain.OpFunctionSetParams},
		{Op: domain.OpSRPolicySet}, {Op: domain.OpSwitcherSelect}, {Op: domain.OpSRPolicySet},
	})
	// One settle after the VVF configs; the failure stops the rest.
	want := []string{"function.set_params", "function.set_params", "settle", "sr_policy.set", "switcher.select"}
	if !slices.Equal(steps, want) {
		t.Fatalf("steps = %v", steps)
	}
	var oe *Error
	if !errors.As(err, &oe) || oe.Timeout || oe.Instruction.Op != domain.OpSwitcherSelect {
		t.Fatalf("err = %v", err)
	}
}

func TestProvisionTimeout(t *testing.T) {
	o := New(Options{Timeout: 10 * time.Millisecond, Drivers: map[domain.Op]Driver{
		domain.OpSRPolicySet: stepDriver{block: true},
	}})
	var oe *Error
	if err := o.Provision(context.Background(), []domain.Instruction{{Op: domain.OpSRPolicySet}}); !errors.As(err, &oe) || !oe.Timeout {
		t.Fatalf("err = %v", err)
	}
}
