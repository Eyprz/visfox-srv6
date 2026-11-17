// Package provision is the configure / provision step of Intent Orchestration (RFC 9315 §5.1.3):
// it hands every rendered instruction to the SFC Provisioner client (internal/sbi) of its kind, in
// order, waits for each reply, and stops at the first failure. Clients talk to
// the lower layer (PolaPCE over gRPC for SR Policies) or only log what they would send (dry run).
package provision

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// Driver sends one instruction to the lower layer: an SBI client (PCE / BGP / VNF Manager NBI) or a
// dry run.
type Driver interface {
	Name() string
	Apply(ctx context.Context, ins domain.Instruction) error
}

// Error is a failed instruction. Timeout tells a timeout (ROUTER_TIMEOUT) from an error reply
// (ROUTER_ERROR).
type Error struct {
	Instruction domain.Instruction
	Driver      string
	Err         error
	Timeout     bool
	// Applied are the instructions that completed before the failure (the network changed by them).
	Applied []domain.Instruction
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s %s via %s: %v", e.Instruction.Op, e.Instruction.Target, e.Driver, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Provisioner routes instructions to drivers by kind.
type Provisioner struct {
	drivers map[domain.Op]Driver
	// settle is the wait after the VVF configs before the switch (vvf_settle_ms).
	settle time.Duration
	// stream is the wait after the SR Policies that start new streams, before the switch
	// (stream_settle_ms).
	stream  time.Duration
	timeout time.Duration
	sleep   func(context.Context, time.Duration) error
}

// Options configure a Provisioner.
type Options struct {
	// Drivers per instruction kind. Kinds without a driver fail.
	Drivers map[domain.Op]Driver
	// Settle is waited after a run of function.set_params, before the next instruction.
	Settle time.Duration
	// StreamSettle is waited after a run of SR Policies marked NewStream, before the next instruction.
	StreamSettle time.Duration
	// Timeout bounds every instruction (the router timeout).
	Timeout time.Duration
}

// New returns a Provisioner.
func New(o Options) *Provisioner {
	return &Provisioner{drivers: o.Drivers, settle: o.Settle, stream: o.StreamSettle, timeout: o.Timeout, sleep: sleepCtx}
}

// Provision runs the instructions in order and stops at the first failure. It waits for every reply
// before the next instruction, and for the settle time after the VVF configs.
func (o *Provisioner) Provision(ctx context.Context, ins []domain.Instruction) error {
	for i, in := range ins {
		d, ok := o.drivers[in.Op]
		if !ok {
			return &Error{Instruction: in, Driver: "-", Err: errors.New("no driver for this instruction"), Applied: ins[:i]}
		}
		if err := o.apply(ctx, d, in); err != nil {
			err.Applied = ins[:i]
			return err
		}
		lastConfig := in.Op == domain.OpFunctionSetParams && (i+1 == len(ins) || ins[i+1].Op != domain.OpFunctionSetParams)
		if lastConfig && o.settle > 0 && i+1 < len(ins) {
			if err := o.sleep(ctx, o.settle); err != nil {
				return &Error{Instruction: in, Driver: d.Name(), Err: err, Applied: ins[:i+1]}
			}
		}
		lastNew := in.NewStream && (i+1 == len(ins) || !ins[i+1].NewStream)
		if lastNew && o.stream > 0 && i+1 < len(ins) {
			if err := o.sleep(ctx, o.stream); err != nil {
				return &Error{Instruction: in, Driver: d.Name(), Err: err, Applied: ins[:i+1]}
			}
		}
	}
	return nil
}

func (o *Provisioner) apply(ctx context.Context, d Driver, in domain.Instruction) *Error {
	if o.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}
	err := d.Apply(ctx, in)
	if err == nil {
		return nil
	}
	return &Error{Instruction: in, Driver: d.Name(), Err: err,
		Timeout: errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
