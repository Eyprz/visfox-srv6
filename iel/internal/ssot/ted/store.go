package ted

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// Save replaces the TED tables with t in one transaction. It uses database/sql only; the driver
// is chosen by the caller (cmd/visfox).
func Save(ctx context.Context, db *sql.DB, t domain.TED) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for _, table := range []string{"sources", "destinations", "ted_sids", "ted_nodes"} {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	for _, n := range t.Nodes {
		if _, err = tx.ExecContext(ctx,
			`INSERT INTO ted_nodes (id, role, label, hostname, router_id, vvf_id) VALUES (?, ?, ?, ?, ?, ?)`,
			n.ID, string(n.Role), n.Label, n.Hostname, nullable(n.RouterID), nullable(n.VVFID)); err != nil {
			return fmt.Errorf("insert node %s: %w", n.ID, err)
		}
		inputs := map[string]domain.InputPort{}
		for _, in := range n.Inputs {
			inputs[in.SID] = in
		}
		for _, sid := range n.SIDs {
			var port, next any
			if in, ok := inputs[sid]; ok {
				port, next = in.Port, in.Next
			}
			if _, err = tx.ExecContext(ctx,
				`INSERT INTO ted_sids (sid, node_id, port, next) VALUES (?, ?, ?, ?)`,
				sid, n.ID, port, next); err != nil {
				return fmt.Errorf("insert SID %s: %w", sid, err)
			}
		}
	}
	for table, eps := range map[string][]domain.Endpoint{"sources": t.Sources, "destinations": t.Destinations} {
		for _, e := range eps {
			if _, err = tx.ExecContext(ctx,
				"INSERT INTO "+table+" (id, name, video_format, node_id) VALUES (?, ?, ?, ?)",
				e.ID, e.Name, e.VideoFormat, e.NodeID); err != nil {
				return fmt.Errorf("insert %s %s: %w", table, e.ID, err)
			}
		}
	}
	return tx.Commit()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
