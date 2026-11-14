// Package ted builds the TED through the Data Source -> YANG -> TED pipeline:
// a Source yields a YANG instance (ietf-network +
// visfox-srv6-ted), the SID interpretation rule derives the roles, and Save persists the result.
package ted

import (
	"context"
	"database/sql"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// Build loads the TED from src, interprets it with rule and bindings, and saves it to db.
func Build(ctx context.Context, src Source, rule Rule, b Bindings, db *sql.DB) (domain.TED, error) {
	dev, err := src.Load(ctx)
	if err != nil {
		return domain.TED{}, err
	}
	t, err := Interpret(dev, rule, b)
	if err != nil {
		return domain.TED{}, err
	}
	if err := Save(ctx, db, t); err != nil {
		return domain.TED{}, err
	}
	return t, nil
}
