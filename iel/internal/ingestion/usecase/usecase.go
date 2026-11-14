// Package usecase is Intent Ingestion (RFC 9315 §5.1.1): the use cases behind the northbound API
// one type per operation (api/openapi.yaml operationId) with a Run method.
//
// Every intent the user enters is held in the SSoT first: presets (logical chains
// and params) and the Active Intent Table (internal/ssot ActiveIntent: one per crosspoint). The use
// cases manage the intent lifecycle (OFF -> STANDBY -> ACTIVE) and drive the fulfillment pipeline
// in its layers:
//
//	ssot.ActiveIntent + preset -> domain.Intent        (here)
//	domain.Intent -> domain.SFCRequest                  (internal/translation)
//	domain.SFCRequest -> SFP -> Plan -> SBI clients      (internal/orchestration)
//
// and report the results north (internal/assurance/report: SSE events, operation log).
//
// The use cases share one environment: the in-memory copy of the SSoT (written through to
// SQLite), the translator, the orchestrator and the reporters, guarded by one mutex.
package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/assurance/monitor"
	"github.com/eyprz/visfox-srv6/iel/internal/assurance/report"
	"github.com/eyprz/visfox-srv6/iel/internal/orchestration"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
	"github.com/eyprz/visfox-srv6/iel/internal/translation"
)

// Options are the dependencies of the use cases.
type Options struct {
	Store     *ssot.Store
	Resources *ssot.Resources
	Catalog   *ssot.Catalog
	// Publisher sends SSE events (internal/assurance/report Broker).
	Publisher report.Publisher
	Logger    *slog.Logger
	// Immutable is reported by GET /config (read-only).
	Immutable apitypes.ImmutableConfig
	// DefaultConfig is used until the settings are changed through the API.
	DefaultConfig apitypes.DynamicConfig
	// Provisioner runs the rendered instructions southbound (required).
	Provisioner orchestration.Provisioner
	// Monitor reports the SR Policies the network holds (Intent Assurance); nil skips the check of
	// the network against the SSoT at startup (dry run).
	Monitor monitor.SRPolicySource
	// Arg is the ARG rule of the SID interpretation rule ("egress", "stream" or "none").
	Arg string
	// ColorBase is the base of the SR Policy colors (one color per OUT).
	ColorBase uint32
	// OnLogLevel is called with the log level at startup and whenever it changes.
	OnLogLevel func(apitypes.LogLevel)
	// Now returns the current time (tests); defaults to time.Now.
	Now func() time.Time
}

// env is what every use case shares. Its methods expect mu to be held.
type env struct {
	mu sync.Mutex

	store     *ssot.Store
	res       *ssot.Resources
	cat       *ssot.Catalog
	tr        translation.Translator
	orch      *orchestration.Orchestrator
	pub       report.Publisher
	oplog     *report.Log
	monitor   monitor.SRPolicySource
	immutable apitypes.ImmutableConfig
	onLevel   func(apitypes.LogLevel)
	now       func() time.Time

	// The SSoT in memory: presets with their layouts (SVoT) and the Active Intent Table.
	presets   []*apitypes.Preset
	layouts   map[string]apitypes.PresetLayout
	cells     map[string]*ssot.ActiveIntent
	cellOrder []string
	// published is the last matrix_update payload per cell, to publish only cells that changed.
	published map[string]string
	config    apitypes.DynamicConfig
}

// UseCases are the northbound use cases, one per operation.
type UseCases struct {
	// Resources (read-only views of the SSoT).
	GetSources       GetSources
	GetDestinations  GetDestinations
	GetSwitchers     GetSwitchers
	GetResourcePools GetResourcePools
	GetTopology      GetTopology
	// Presets: the logical chains the user designs.
	GetPresets                GetPresets
	GetPresetByID             GetPresetByID
	GetPresetConstraints      GetPresetConstraints
	ValidatePreset            ValidatePreset
	CreatePreset              CreatePreset
	UpdatePreset              UpdatePreset
	DeletePreset              DeletePreset
	PatchPresetFunctionParams PatchPresetFunctionParams
	GetPresetLayout           GetPresetLayout
	SavePresetLayout          SavePresetLayout
	// Functions: the catalog.
	GetFunctions           GetFunctions
	GetFunctionSchema      GetFunctionSchema
	ValidateFunctionParams ValidateFunctionParams
	// Matrix: the crosspoint intents and their lifecycle.
	GetMatrixState           GetMatrixState
	GetMatrixCell            GetMatrixCell
	ValidateMatrixAssignment ValidateMatrixAssignment
	ApplyPresetToCell        ApplyPresetToCell
	ClearCellPreset          ClearCellPreset
	TakeCell                 TakeCell
	StandbyCell              StandbyCell
	GetCellPath              GetCellPath
	// System.
	GetConfig    GetConfig
	UpdateConfig UpdateConfig
	GetLogs      GetLogs
}

// New loads the SSoT, creates the intents of every IN x OUT and plans the reserved streams.
func New(ctx context.Context, o Options) (*UseCases, error) {
	if o.Provisioner == nil {
		return nil, fmt.Errorf("usecase: no provisioner")
	}
	// Pinned VVFs list their TED nodes (GET /presets/constraints, chain validation).
	cat := o.Catalog.WithNodes(o.Resources.VVFNodes)
	required := cat.Constraints.Domain.SwitcherRequired != nil && *cat.Constraints.Domain.SwitcherRequired
	s := &env{
		store: o.Store, res: o.Resources, cat: cat, tr: translation.Translator{Catalog: cat},
		orch: orchestration.New(orchestration.Options{Resources: o.Resources, SwitcherRequired: required,
			Arg: o.Arg, ColorBase: o.ColorBase, Provisioner: o.Provisioner}),
		pub: o.Publisher, monitor: o.Monitor, immutable: o.Immutable, onLevel: o.OnLogLevel, now: o.Now,
		layouts: map[string]apitypes.PresetLayout{}, cells: map[string]*ssot.ActiveIntent{}, published: map[string]string{},
		config: o.DefaultConfig,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.onLevel == nil {
		s.onLevel = func(apitypes.LogLevel) {}
	}

	presets, err := s.store.Presets(ctx)
	if err != nil {
		return nil, fmt.Errorf("load presets: %w", err)
	}
	for i := range presets {
		s.presets = append(s.presets, &presets[i])
	}
	layouts, err := s.store.Layouts(ctx)
	if err != nil {
		return nil, fmt.Errorf("load layouts: %w", err)
	}
	for _, l := range layouts {
		s.layouts[l.PresetId] = l
	}
	if cfg, err := s.store.DynamicConfig(ctx); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	} else if cfg != nil {
		s.config = *cfg
	}
	s.oplog = report.NewLog(o.Logger, o.Publisher, s.now, s.config.LogLevel)
	s.onLevel(s.config.LogLevel)

	if err := s.loadIntents(ctx); err != nil {
		return nil, err
	}
	// What orchestration selected and provisioned before the restart: the paths on air stay.
	realized, err := s.store.Realization(ctx)
	if err != nil {
		return nil, fmt.Errorf("load realization: %w", err)
	}
	s.orch.Load(realized)
	s.refresh(ctx)
	s.assure(ctx)
	for _, id := range s.cellOrder {
		s.published[id] = s.viewJSON(s.cells[id])
	}
	return &UseCases{
		GetSources: GetSources{s}, GetDestinations: GetDestinations{s}, GetSwitchers: GetSwitchers{s},
		GetResourcePools: GetResourcePools{s}, GetTopology: GetTopology{s},
		GetPresets: GetPresets{s}, GetPresetByID: GetPresetByID{s}, GetPresetConstraints: GetPresetConstraints{s},
		ValidatePreset: ValidatePreset{s}, CreatePreset: CreatePreset{s}, UpdatePreset: UpdatePreset{s},
		DeletePreset: DeletePreset{s}, PatchPresetFunctionParams: PatchPresetFunctionParams{s},
		GetPresetLayout: GetPresetLayout{s}, SavePresetLayout: SavePresetLayout{s},
		GetFunctions: GetFunctions{s}, GetFunctionSchema: GetFunctionSchema{s}, ValidateFunctionParams: ValidateFunctionParams{s},
		GetMatrixState: GetMatrixState{s}, GetMatrixCell: GetMatrixCell{s}, ValidateMatrixAssignment: ValidateMatrixAssignment{s},
		ApplyPresetToCell: ApplyPresetToCell{s}, ClearCellPreset: ClearCellPreset{s}, TakeCell: TakeCell{s},
		StandbyCell: StandbyCell{s}, GetCellPath: GetCellPath{s},
		GetConfig: GetConfig{s}, UpdateConfig: UpdateConfig{s}, GetLogs: GetLogs{s},
	}, nil
}

// CellID is the cell id convention of api/openapi.yaml (CellId): in-{n} x out-{m} -> IN{n}-OUT{m}.
func CellID(sourceID, destinationID string) string {
	return "IN" + strings.TrimPrefix(sourceID, "in-") + "-OUT" + strings.TrimPrefix(destinationID, "out-")
}

// loadIntents keeps the stored intents of the current IN x OUT, creates the missing ones (OFF), and
// drops intents whose IN or OUT left the TED.
func (s *env) loadIntents(ctx context.Context) error {
	stored, err := s.store.ActiveIntents(ctx)
	if err != nil {
		return fmt.Errorf("load intents: %w", err)
	}
	byID := map[string]ssot.ActiveIntent{}
	for _, c := range stored {
		byID[c.ID] = c
	}
	for _, src := range s.res.Sources {
		for _, dst := range s.res.Destinations {
			id := CellID(src.Id, dst.Id)
			c, ok := byID[id]
			delete(byID, id)
			if !ok || c.SourceID != src.Id || c.DestinationID != dst.Id {
				c = ssot.ActiveIntent{ID: id, SourceID: src.Id, DestinationID: dst.Id, State: apitypes.ExecutionStateOff, UpdatedAt: s.now()}
				if err := s.store.PutActiveIntent(ctx, c); err != nil {
					return err
				}
			}
			if c.PresetID != nil && s.preset(*c.PresetID) == nil {
				// The preset vanished (e.g. a reset seed): the intent has nothing to run.
				c.PresetID, c.State, c.Health = nil, apitypes.ExecutionStateOff, nil
				if err := s.store.PutActiveIntent(ctx, c); err != nil {
					return err
				}
			}
			s.cells[id] = &c
			s.cellOrder = append(s.cellOrder, id)
		}
	}
	for id := range byID {
		if err := s.store.DeleteActiveIntent(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- intents and events

func (s *env) preset(id string) *apitypes.Preset {
	i := slices.IndexFunc(s.presets, func(p *apitypes.Preset) bool { return p.Id == id })
	if i < 0 {
		return nil
	}
	return s.presets[i]
}

// view is a cell as the API returns it: the preset's current chain is the live chain.
func (s *env) view(c *ssot.ActiveIntent) apitypes.MatrixCell {
	updated := c.UpdatedAt
	issues := s.takeIssues(c)
	chain := []apitypes.FunctionInstance{}
	mc := apitypes.MatrixCell{
		CellId: c.ID, SourceId: c.SourceID, DestinationId: c.DestinationID, ExecutionState: c.State,
		HealthState: c.Health, LastRollback: c.LastRollback, UpdatedAt: &updated, TakeIssues: &issues,
	}
	if c.PresetID != nil {
		if p := s.preset(*c.PresetID); p != nil {
			id, name := p.Id, p.Name
			mc.PresetId, mc.PresetName = &id, &name
			chain = clone(p.Chain)
		}
	}
	mc.LiveChain = &chain
	return mc
}

func (s *env) viewJSON(c *ssot.ActiveIntent) string {
	b, _ := json.Marshal(s.view(c))
	return string(b)
}

// touch marks an intent changed and writes it to the SSoT.
func (s *env) touch(ctx context.Context, c *ssot.ActiveIntent) error {
	c.UpdatedAt = s.now()
	return s.store.PutActiveIntent(ctx, *c)
}

// sync publishes matrix_update for every cell whose API view changed (including take_issues that
// changed because another cell moved).
func (s *env) sync() {
	for _, id := range s.cellOrder {
		c := s.cells[id]
		v := s.viewJSON(c)
		if s.published[id] == v {
			continue
		}
		s.published[id] = v
		s.publish(string(apitypes.MatrixUpdateEventTypeMatrixUpdate), apitypes.MatrixUpdateEvent{
			Type: apitypes.MatrixUpdateEventTypeMatrixUpdate, Cell: s.view(c),
		})
	}
}

func (s *env) publish(eventType string, payload any) {
	if s.pub != nil {
		s.pub.Publish(eventType, payload)
	}
}

// cellsUsing returns the applied (standby / active) intents that reference the preset, in matrix
// order.
func (s *env) cellsUsing(presetID string) []*ssot.ActiveIntent {
	var out []*ssot.ActiveIntent
	for _, id := range s.cellOrder {
		c := s.cells[id]
		if c.PresetID != nil && *c.PresetID == presetID &&
			(c.State == apitypes.ExecutionStateStandby || c.State == apitypes.ExecutionStateActive) {
			out = append(out, c)
		}
	}
	return out
}

// log records an operation log (GET /logs, SSE log) when it passes the configured level.
func (s *env) log(level apitypes.LogLevel, message, cellID string, code string) {
	s.oplog.Record(level, message, cellID, code)
}

// GetLogs returns the newest logs first, at or above level, optionally for one cell.
// It is getLogs in api/openapi.yaml.
type GetLogs struct{ env *env }

// Run runs the use case.
func (u GetLogs) Run(level *apitypes.LogLevel, cellID string, limit int) []apitypes.LogEntry {
	return u.env.oplog.Entries(level, cellID, limit)
}

// clone deep-copies an API model through JSON.
func clone[T any](v T) T {
	var out T
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

func ptr[T any](v T) *T { return &v }
