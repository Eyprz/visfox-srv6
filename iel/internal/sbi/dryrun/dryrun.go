// Package dryrun is the dry-run client of the SFC Provisioner: it logs the instructions instead of
// sending them (orchestrator.sr_policy: dryrun, and switchers / VVF nodes until their VNF Manager
// client exists).
package dryrun

import (
	"context"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// Client logs instructions instead of sending them.
type Client struct {
	Log func(message string)
}

// Name implements provision.Driver.
func (Client) Name() string { return "dryrun" }

// Apply implements provision.Driver.
func (d Client) Apply(_ context.Context, in domain.Instruction) error {
	if d.Log != nil {
		d.Log("dryrun: " + in.Describe())
	}
	return nil
}
