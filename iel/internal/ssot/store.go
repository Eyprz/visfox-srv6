package ssot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// execer is a database or a transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Store persists the SSoT the user's intents form (presets and the Active Intent Table) and the
// settings in SQLite (iel/db/schema.sql). It uses database/sql only; the driver is chosen by the
// caller (cmd/visfox).
type Store struct {
	db *sql.DB
}

// NewStore returns a store on an open database with the schema applied.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// ActiveIntent is a row of the Active Intent Table: the user's intent
// for one crosspoint (cell) of the matrix: which preset it carries and whether it is reserved
// (standby) or on air (active), with the health Assurance reports and the last rollback. The preset
// name and live chain are not stored: intents reference their preset. Stored in matrix_cells.
type ActiveIntent struct {
	ID            string
	SourceID      string
	DestinationID string
	State         apitypes.ExecutionState
	Health        *apitypes.HealthState
	PresetID      *string
	LastRollback  *apitypes.RollbackInfo
	// LKG is the last known good config: the preset and live chain of the last change that was
	// provisioned without error. Rollbacks restore it.
	LKG       *apitypes.CellConfigSnapshot
	UpdatedAt time.Time
}

const (
	keyDynamicConfig = "dynamic_config"
	keyPresetsSeeded = "presets_seeded"
)

// SeedPresets inserts the seed on the first start (once per database) and reports whether it did.
func (s *Store) SeedPresets(ctx context.Context, seed *PresetSeed, now time.Time) (bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, keyPresetsSeeded).Scan(&v)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	for _, p := range seed.Presets {
		p.CreatedAt, p.UpdatedAt = &now, &now
		if err := s.PutPreset(ctx, p); err != nil {
			return false, err
		}
	}
	for _, l := range seed.Layouts {
		if err := s.PutLayout(ctx, l); err != nil {
			return false, err
		}
	}
	return true, s.putMeta(ctx, keyPresetsSeeded, now.Format(time.RFC3339))
}

// SyncPresets makes the stored presets and their layouts equal to the seed, the predefined presets:
// it adds or updates the seed presets (updated_at changes only when a
// preset differs), deletes the others and turns the cells that used them OFF. It returns the ids of
// the deleted presets.
func (s *Store) SyncPresets(ctx context.Context, seed *PresetSeed, now time.Time) ([]string, error) {
	stored, err := s.Presets(ctx)
	if err != nil {
		return nil, err
	}
	old := map[string]apitypes.Preset{}
	for _, p := range stored {
		old[p.Id] = p
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, p := range seed.Presets {
		p.CreatedAt, p.UpdatedAt = &now, &now
		if o, ok := old[p.Id]; ok {
			p.CreatedAt, p.UpdatedAt = o.CreatedAt, o.UpdatedAt
			if !samePreset(o, p) {
				p.UpdatedAt = &now
			}
			delete(old, p.Id)
		}
		if err := putPreset(ctx, tx, p); err != nil {
			return nil, err
		}
	}
	removed := []string{}
	for id := range old {
		if _, err := tx.ExecContext(ctx, `UPDATE matrix_cells SET preset_id = NULL, execution_state = ?,
			health_state = NULL, updated_at = ? WHERE preset_id = ?`,
			string(apitypes.ExecutionStateOff), formatTime(now), id); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM presets WHERE id = ?`, id); err != nil {
			return nil, err
		}
		removed = append(removed, id)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM preset_layouts`); err != nil {
		return nil, err
	}
	for _, l := range seed.Layouts {
		if err := putLayout(ctx, tx, l); err != nil {
			return nil, err
		}
	}
	if err := putMeta(ctx, tx, keyPresetsSeeded, now.Format(time.RFC3339)); err != nil {
		return nil, err
	}
	sort.Strings(removed)
	return removed, tx.Commit()
}

// samePreset reports whether two presets have the same name, description, chain and SLA.
func samePreset(a, b apitypes.Preset) bool {
	a.CreatedAt, a.UpdatedAt, b.CreatedAt, b.UpdatedAt = nil, nil, nil, nil
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}

// Presets returns every preset in creation order.
func (s *Store) Presets(ctx context.Context) ([]apitypes.Preset, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, description, chain_json, sla_json, created_at, updated_at FROM presets ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []apitypes.Preset
	for rows.Next() {
		var p apitypes.Preset
		var desc, sla sql.NullString
		var chain, created, updated string
		if err := rows.Scan(&p.Id, &p.Name, &desc, &chain, &sla, &created, &updated); err != nil {
			return nil, err
		}
		if desc.Valid {
			p.Description = &desc.String
		}
		if err := json.Unmarshal([]byte(chain), &p.Chain); err != nil {
			return nil, fmt.Errorf("preset %s chain: %w", p.Id, err)
		}
		if sla.Valid {
			if err := json.Unmarshal([]byte(sla.String), &p.DefaultSla); err != nil {
				return nil, fmt.Errorf("preset %s SLA: %w", p.Id, err)
			}
		}
		if p.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if p.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PutPreset inserts or replaces a preset (in_use_cell_ids is derived and not stored).
func (s *Store) PutPreset(ctx context.Context, p apitypes.Preset) error {
	return putPreset(ctx, s.db, p)
}

func putPreset(ctx context.Context, db execer, p apitypes.Preset) error {
	chain, err := json.Marshal(p.Chain)
	if err != nil {
		return err
	}
	var sla any
	if p.DefaultSla != nil {
		b, err := json.Marshal(p.DefaultSla)
		if err != nil {
			return err
		}
		sla = string(b)
	}
	now := time.Now().UTC()
	created, updated := now, now
	if p.CreatedAt != nil {
		created = *p.CreatedAt
	}
	if p.UpdatedAt != nil {
		updated = *p.UpdatedAt
	}
	_, err = db.ExecContext(ctx, `INSERT INTO presets (id, name, description, chain_json, sla_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, description = excluded.description,
			chain_json = excluded.chain_json, sla_json = excluded.sla_json, updated_at = excluded.updated_at`,
		p.Id, p.Name, p.Description, string(chain), sla, formatTime(created), formatTime(updated))
	if err != nil {
		return fmt.Errorf("save preset %s: %w", p.Id, err)
	}
	return nil
}

// DeletePreset removes a preset and its layout.
func (s *Store) DeletePreset(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM presets WHERE id = ?`, id)
	return err
}

// Layouts returns every preset layout.
func (s *Store) Layouts(ctx context.Context) ([]apitypes.PresetLayout, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT layout_json FROM preset_layouts ORDER BY preset_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []apitypes.PresetLayout
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var l apitypes.PresetLayout
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// PutLayout inserts or replaces a preset layout.
func (s *Store) PutLayout(ctx context.Context, l apitypes.PresetLayout) error {
	return putLayout(ctx, s.db, l)
}

func putLayout(ctx context.Context, db execer, l apitypes.PresetLayout) error {
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO preset_layouts (preset_id, layout_json) VALUES (?, ?)
		ON CONFLICT(preset_id) DO UPDATE SET layout_json = excluded.layout_json`, l.PresetId, string(raw))
	return err
}

// ActiveIntents returns every crosspoint intent.
func (s *Store) ActiveIntents(ctx context.Context) ([]ActiveIntent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT cell_id, source_id, destination_id, execution_state, health_state,
		preset_id, last_rollback_json, lkg_json, updated_at FROM matrix_cells`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ActiveIntent
	for rows.Next() {
		var c ActiveIntent
		var state, updated string
		var health, preset, rollback, lkg sql.NullString
		if err := rows.Scan(&c.ID, &c.SourceID, &c.DestinationID, &state, &health, &preset, &rollback, &lkg, &updated); err != nil {
			return nil, err
		}
		c.State = apitypes.ExecutionState(state)
		if health.Valid {
			h := apitypes.HealthState(health.String)
			c.Health = &h
		}
		if preset.Valid {
			c.PresetID = &preset.String
		}
		if rollback.Valid {
			if err := json.Unmarshal([]byte(rollback.String), &c.LastRollback); err != nil {
				return nil, fmt.Errorf("cell %s rollback: %w", c.ID, err)
			}
		}
		if lkg.Valid {
			if err := json.Unmarshal([]byte(lkg.String), &c.LKG); err != nil {
				return nil, fmt.Errorf("cell %s LKG: %w", c.ID, err)
			}
		}
		t, err := parseTime(updated)
		if err != nil {
			return nil, err
		}
		c.UpdatedAt = *t
		out = append(out, c)
	}
	return out, rows.Err()
}

// PutActiveIntent inserts or replaces a crosspoint intent.
func (s *Store) PutActiveIntent(ctx context.Context, c ActiveIntent) error {
	rollback, err := nullJSON(c.LastRollback)
	if err != nil {
		return err
	}
	lkg, err := nullJSON(c.LKG)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO matrix_cells (cell_id, source_id, destination_id, execution_state,
		health_state, preset_id, last_rollback_json, lkg_json, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cell_id) DO UPDATE SET source_id = excluded.source_id, destination_id = excluded.destination_id,
			execution_state = excluded.execution_state, health_state = excluded.health_state,
			preset_id = excluded.preset_id, last_rollback_json = excluded.last_rollback_json,
			lkg_json = excluded.lkg_json, updated_at = excluded.updated_at`,
		c.ID, c.SourceID, c.DestinationID, string(c.State), c.Health, c.PresetID, rollback, lkg, formatTime(c.UpdatedAt))
	if err != nil {
		return fmt.Errorf("save cell %s: %w", c.ID, err)
	}
	return nil
}

// DeleteActiveIntent removes a crosspoint intent (its IN or OUT left the TED).
func (s *Store) DeleteActiveIntent(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM matrix_cells WHERE cell_id = ?`, id)
	return err
}

// nullJSON is v as JSON text, or NULL for a nil pointer.
func nullJSON[T any](v *T) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Realization is what Intent Orchestration derived from the intents and provisioned: the SFP of
// every OUT, the config every VVF node runs and the SR Policies on the headends. It is part of the
// SSoT so a restart neither loses nor guesses it, and Assurance compares it with the network.
type Realization struct {
	SFPs       map[string]domain.SFP
	VVFConfigs map[string]string
	Policies   []domain.SRPolicy
}

// Realization reads the stored realization.
func (s *Store) Realization(ctx context.Context) (Realization, error) {
	r := Realization{SFPs: map[string]domain.SFP{}, VVFConfigs: map[string]string{}}
	rows, err := s.db.QueryContext(ctx, `SELECT destination_id, sfp_json FROM stream_paths`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var id, raw string
		var sfp domain.SFP
		if err := rows.Scan(&id, &raw); err != nil {
			_ = rows.Close()
			return r, err
		}
		if err := json.Unmarshal([]byte(raw), &sfp); err != nil {
			_ = rows.Close()
			return r, fmt.Errorf("stream path %s: %w", id, err)
		}
		r.SFPs[id] = sfp
	}
	if err := closeRows(rows); err != nil {
		return r, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT node_id, config FROM vvf_node_configs`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var id, config string
		if err := rows.Scan(&id, &config); err != nil {
			_ = rows.Close()
			return r, err
		}
		r.VVFConfigs[id] = config
	}
	if err := closeRows(rows); err != nil {
		return r, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT headend, color, endpoint, name, segment_list FROM sr_policies
		ORDER BY headend, color, endpoint`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var p domain.SRPolicy
		var segs string
		if err := rows.Scan(&p.Headend, &p.Color, &p.Endpoint, &p.Name, &segs); err != nil {
			_ = rows.Close()
			return r, err
		}
		if err := json.Unmarshal([]byte(segs), &p.SegmentList); err != nil {
			_ = rows.Close()
			return r, fmt.Errorf("SR Policy %s: %w", p.Name, err)
		}
		r.Policies = append(r.Policies, p)
	}
	return r, closeRows(rows)
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}

// PutRealization replaces the stored realization.
func (s *Store) PutRealization(ctx context.Context, r Realization, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{`DELETE FROM stream_paths`, `DELETE FROM vvf_node_configs`, `DELETE FROM sr_policies`} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	at := formatTime(now)
	for id, sfp := range r.SFPs {
		raw, err := json.Marshal(sfp)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO stream_paths (destination_id, sfp_json, updated_at) VALUES (?, ?, ?)`,
			id, string(raw), at); err != nil {
			return err
		}
	}
	for id, config := range r.VVFConfigs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO vvf_node_configs (node_id, config, updated_at) VALUES (?, ?, ?)`,
			id, config, at); err != nil {
			return err
		}
	}
	for _, p := range r.Policies {
		segs, err := json.Marshal(p.SegmentList)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sr_policies (headend, color, endpoint, name, segment_list, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`, p.Headend, p.Color, p.Endpoint, p.Name, string(segs), at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DynamicConfig returns the saved settings, or nil if they were never changed.
func (s *Store) DynamicConfig(ctx context.Context) (*apitypes.DynamicConfig, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, keyDynamicConfig).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c apitypes.DynamicConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("dynamic config: %w", err)
	}
	return &c, nil
}

// PutDynamicConfig saves the settings.
func (s *Store) PutDynamicConfig(ctx context.Context, c apitypes.DynamicConfig) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.putMeta(ctx, keyDynamicConfig, string(raw))
}

func (s *Store) putMeta(ctx context.Context, key, value string) error {
	return putMeta(ctx, s.db, key, value)
}

func putMeta(ctx context.Context, db execer, key, value string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) (*time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil, fmt.Errorf("parse time %q: %w", s, err)
	}
	return &t, nil
}
