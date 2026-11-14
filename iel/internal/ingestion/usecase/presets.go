package usecase

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// presetView is a preset as the API returns it (with the derived in_use_cell_ids).
func (s *env) presetView(p *apitypes.Preset) apitypes.Preset {
	v := clone(*p)
	ids := []string{}
	for _, c := range s.cellsUsing(p.Id) {
		ids = append(ids, c.ID)
	}
	v.InUseCellIds = &ids
	return v
}

// GetPresets returns every preset.
// It is getPresets in api/openapi.yaml.
type GetPresets struct{ env *env }

// Run runs the use case.
func (u GetPresets) Run() []apitypes.Preset {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []apitypes.Preset{}
	for _, p := range s.presets {
		out = append(out, s.presetView(p))
	}
	return out
}

// GetPresetByID returns one preset.
// It is getPresetById in api/openapi.yaml.
type GetPresetByID struct{ env *env }

// Run runs the use case.
func (u GetPresetByID) Run(id string) (apitypes.Preset, error) {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.preset(id)
	if p == nil {
		return apitypes.Preset{}, domain.NotFound("Preset")
	}
	return s.presetView(p), nil
}

// GetPresetConstraints returns the chain rules and the domain policy.
// It is getPresetConstraints in api/openapi.yaml.
type GetPresetConstraints struct{ env *env }

// Run runs the use case.
func (u GetPresetConstraints) Run() apitypes.ChainConstraints {
	s := u.env
	return clone(s.cat.Constraints)
}

// ValidatePreset checks a draft chain (POST /presets/validate). Stage A reports no resource estimate.
// It is validatePreset in api/openapi.yaml.
type ValidatePreset struct{ env *env }

// Run runs the use case.
func (u ValidatePreset) Run(in apitypes.PresetInput) apitypes.PresetValidationResult {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	issues, out := s.tr.CheckChain(in.Chain)
	compatible := []string{}
	for _, id := range s.cellOrder {
		c := s.cells[id]
		if c.State == apitypes.ExecutionStateDisconnected {
			continue
		}
		src, _ := s.res.Source(c.SourceID)
		dst, _ := s.res.Destination(c.DestinationID)
		if s.tr.CheckFormat(apitypes.VideoFormat(src.VideoFormat), apitypes.VideoFormat(dst.VideoFormat), in.Chain) == nil {
			compatible = append(compatible, id)
		}
	}
	r := apitypes.PresetValidationResult{Valid: len(issues) == 0, Issues: &issues, CompatibleCellIds: &compatible}
	if out != nil {
		r.OutputFormat = &apitypes.MediaFormat{VideoFormat: *out}
	}
	return r
}

func invalidChain(issues []apitypes.ChainIssue) *domain.Error {
	return &domain.Error{Code: apitypes.ErrorCodeINVALIDCHAIN, Message: "Chain violates constraints",
		Details: map[string]any{"issues": issues}}
}

// nextPresetID returns preset-<n> above every numbered preset id.
func (s *env) nextPresetID() string {
	n := 0
	for _, p := range s.presets {
		if v, err := strconv.Atoi(strings.TrimPrefix(p.Id, "preset-")); err == nil && v > n {
			n = v
		}
	}
	return "preset-" + strconv.Itoa(n+1)
}

// errEditing answers preset writes while the IEL serves the predefined presets only.
var errEditing = &domain.Error{Code: apitypes.ErrorCodeNOTIMPLEMENTED,
	Message: "Preset editing is not implemented yet: this IEL serves the predefined presets " +
		"(iel/db/presets.yaml) until intent translation (stage C). Parameter changes are available."}

// CreatePreset saves a new preset after checking its chain.
// It is createPreset in api/openapi.yaml.
type CreatePreset struct{ env *env }

// Run runs the use case.
func (u CreatePreset) Run(ctx context.Context, in apitypes.PresetInput) (apitypes.Preset, error) {
	s := u.env
	if !s.editing {
		return apitypes.Preset{}, errEditing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if issues, _ := s.tr.CheckChain(in.Chain); len(issues) > 0 {
		return apitypes.Preset{}, invalidChain(issues)
	}
	now := s.now()
	p := &apitypes.Preset{
		Id: s.nextPresetID(), Name: in.Name, Description: in.Description, Chain: clone(in.Chain),
		DefaultSla: in.DefaultSla, CreatedAt: &now, UpdatedAt: &now,
	}
	if err := s.store.PutPreset(ctx, *p); err != nil {
		return apitypes.Preset{}, domain.Internal(err)
	}
	s.presets = append(s.presets, p)
	s.publishPreset(apitypes.PresetChangedEventActionCreated, p.Id)
	s.log(apitypes.LogLevelInfo, "Preset '"+p.Name+"' created", "", "")
	return s.presetView(p), nil
}

// structureChanged reports whether the instance ids, types, pinned nodes or order of the chain differ.
func structureChanged(a, b []apitypes.FunctionInstance) bool {
	node := func(f apitypes.FunctionInstance) string {
		if f.VvfNodeId == nil {
			return ""
		}
		return *f.VvfNodeId
	}
	return !slices.EqualFunc(a, b, func(x, y apitypes.FunctionInstance) bool {
		return x.InstanceId == y.InstanceId && x.Type == y.Type && node(x) == node(y)
	})
}

// UpdatePreset replaces a preset. While cells use it only the params may change; they apply to
// those cells at once.
// It is updatePreset in api/openapi.yaml.
type UpdatePreset struct{ env *env }

// Run runs the use case.
func (u UpdatePreset) Run(ctx context.Context, id string, in apitypes.PresetInput) (apitypes.Preset, error) {
	s := u.env
	if !s.editing {
		return apitypes.Preset{}, errEditing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.preset(id)
	if p == nil {
		return apitypes.Preset{}, domain.NotFound("Preset")
	}
	if using := s.cellsUsing(id); len(using) > 0 && structureChanged(p.Chain, in.Chain) {
		ids := cellIDs(using)
		return apitypes.Preset{}, &domain.Error{Code: apitypes.ErrorCodePRESETINUSE,
			Message: "Chain structure cannot change while applied to " + strings.Join(ids, ", "),
			Details: map[string]any{"cell_ids": ids}}
	}
	if issues, _ := s.tr.CheckChain(in.Chain); len(issues) > 0 {
		return apitypes.Preset{}, invalidChain(issues)
	}
	now := s.now()
	next := &apitypes.Preset{
		Id: id, Name: in.Name, Description: in.Description, Chain: clone(in.Chain), DefaultSla: in.DefaultSla,
		CreatedAt: p.CreatedAt, UpdatedAt: &now,
	}
	if err := s.store.PutPreset(ctx, *next); err != nil {
		return apitypes.Preset{}, domain.Internal(err)
	}
	*p = *next
	if err := s.touchUsing(ctx, id); err != nil {
		return apitypes.Preset{}, err
	}
	s.publishPreset(apitypes.PresetChangedEventActionUpdated, id)
	s.log(apitypes.LogLevelInfo, "Preset '"+p.Name+"' updated", "", "")
	return s.presetView(p), nil
}

// touchUsing marks the cells using a preset changed (their live chain follows the preset).
func (s *env) touchUsing(ctx context.Context, presetID string) error {
	for _, c := range s.cellsUsing(presetID) {
		if err := s.touch(ctx, c); err != nil {
			return domain.Internal(err)
		}
	}
	s.sync()
	return nil
}

// DeletePreset removes a preset that no cell references.
// It is deletePreset in api/openapi.yaml.
type DeletePreset struct{ env *env }

// Run runs the use case.
func (u DeletePreset) Run(ctx context.Context, id string) error {
	s := u.env
	if !s.editing {
		return errEditing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.preset(id)
	if p == nil {
		return domain.NotFound("Preset")
	}
	var ids []string
	for _, cid := range s.cellOrder {
		if c := s.cells[cid]; c.PresetID != nil && *c.PresetID == id {
			ids = append(ids, cid)
		}
	}
	if len(ids) > 0 {
		return &domain.Error{Code: apitypes.ErrorCodePRESETINUSE,
			Message: "Preset " + id + " is applied to " + strings.Join(ids, ", "),
			Details: map[string]any{"cell_ids": ids}}
	}
	if err := s.store.DeletePreset(ctx, id); err != nil {
		return domain.Internal(err)
	}
	s.presets = slices.DeleteFunc(s.presets, func(x *apitypes.Preset) bool { return x.Id == id })
	delete(s.layouts, id)
	s.publishPreset(apitypes.PresetChangedEventActionDeleted, id)
	s.log(apitypes.LogLevelInfo, "Preset '"+p.Name+"' deleted", "", "")
	return nil
}

// PatchPresetFunctionParams merges params into one function of a preset (PATCH /presets/{id}/functions/{instanceId}).
// The change applies to every cell using the preset: on-air cells get the new VVF config at once
// (southbound, before the preset is saved), standby cells at their next Take.
// It is patchPresetFunctionParams in api/openapi.yaml.
type PatchPresetFunctionParams struct{ env *env }

// Run runs the use case.
func (u PatchPresetFunctionParams) Run(ctx context.Context, presetID, instanceID string, params map[string]any) (apitypes.PresetFunctionUpdateResult, error) {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.preset(presetID)
	if p == nil {
		return apitypes.PresetFunctionUpdateResult{}, domain.NotFound("Preset")
	}
	i := slices.IndexFunc(p.Chain, func(f apitypes.FunctionInstance) bool { return f.InstanceId == instanceID })
	if i < 0 {
		return apitypes.PresetFunctionUpdateResult{}, domain.NotFound("Function instance")
	}
	merged := maps.Clone(p.Chain[i].Params)
	if merged == nil {
		merged = map[string]any{}
	}
	maps.Copy(merged, params)
	if errs := s.tr.CheckInterface(p.Chain[i].Type, merged); len(errs) > 0 {
		return apitypes.PresetFunctionUpdateResult{}, &domain.Error{Code: apitypes.ErrorCodeINVALIDPARAMS,
			Message: "Invalid params", Details: map[string]any{"errors": errs}}
	}
	next := clone(*p)
	next.Chain[i].Params = merged
	now := s.now()
	next.UpdatedAt = &now
	using := s.cellsUsing(presetID)
	for _, c := range using {
		if c.State == apitypes.ExecutionStateActive {
			if err := s.reapply(ctx, c, next.Chain); err != nil {
				// The preset keeps its params; every cell using it goes back to its LKG.
				name := string(p.Chain[i].Type)
				if f, ok := s.cat.Function(p.Chain[i].Type); ok {
					name = f.Name
				}
				attempts := map[string]apitypes.CellConfigSnapshot{}
				for _, u := range using {
					attempts[u.ID] = s.snapshot(next.Id, next.Chain)
				}
				err = s.rollBack(ctx, using, attempts, fmt.Sprintf("%s params of '%s'", name, p.Name), err)
				s.publishPreset(apitypes.PresetChangedEventActionUpdated, presetID)
				return apitypes.PresetFunctionUpdateResult{}, err
			}
		}
	}
	if err := s.store.PutPreset(ctx, next); err != nil {
		return apitypes.PresetFunctionUpdateResult{}, domain.Internal(err)
	}
	*p = next
	for _, c := range using {
		s.markGood(c)
	}
	s.refresh(ctx)
	if err := s.touchUsing(ctx, presetID); err != nil {
		return apitypes.PresetFunctionUpdateResult{}, err
	}
	s.publishPreset(apitypes.PresetChangedEventActionUpdated, presetID)
	return apitypes.PresetFunctionUpdateResult{Instance: clone(p.Chain[i]), AppliedCellIds: cellIDsOrEmpty(using)}, nil
}

func (s *env) publishPreset(action apitypes.PresetChangedEventAction, id string) {
	s.publish(string(apitypes.PresetChangedEventTypePresetChanged), apitypes.PresetChangedEvent{
		Type: apitypes.PresetChangedEventTypePresetChanged, Action: action, PresetId: id,
	})
}

// GetPresetLayout returns the designer layout of a preset (SVoT).
// It is getPresetLayout in api/openapi.yaml.
type GetPresetLayout struct{ env *env }

// Run runs the use case.
func (u GetPresetLayout) Run(presetID string) (apitypes.PresetLayout, error) {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.layouts[presetID]
	if !ok {
		return apitypes.PresetLayout{}, domain.NotFound("Layout")
	}
	return clone(l), nil
}

// SavePresetLayout saves the designer layout of a preset.
// It is savePresetLayout in api/openapi.yaml.
type SavePresetLayout struct{ env *env }

// Run runs the use case.
func (u SavePresetLayout) Run(ctx context.Context, presetID string, l apitypes.PresetLayout) (apitypes.PresetLayout, error) {
	s := u.env
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.preset(presetID) == nil {
		return apitypes.PresetLayout{}, domain.NotFound("Preset")
	}
	l.PresetId = presetID
	if err := s.store.PutLayout(ctx, l); err != nil {
		return apitypes.PresetLayout{}, domain.Internal(err)
	}
	s.layouts[presetID] = clone(l)
	return l, nil
}
