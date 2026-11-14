package ssot

import (
	"fmt"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"
)

// PresetSeed is the initial preset set (iel/db/presets.yaml), inserted on the first start only.
type PresetSeed struct {
	Presets []apitypes.Preset       `json:"presets"`
	Layouts []apitypes.PresetLayout `json:"layouts"`
}

// LoadPresetSeed reads the initial presets and layouts.
func LoadPresetSeed(path string) (*PresetSeed, error) {
	var s PresetSeed
	if err := decodeYAML(path, &s); err != nil {
		return nil, fmt.Errorf("preset seed: %w", err)
	}
	ids := map[string]bool{}
	for _, p := range s.Presets {
		if p.Id == "" || ids[p.Id] {
			return nil, fmt.Errorf("preset seed %s: missing or duplicate preset id %q", path, p.Id)
		}
		ids[p.Id] = true
	}
	for _, l := range s.Layouts {
		if !ids[l.PresetId] {
			return nil, fmt.Errorf("preset seed %s: layout for unknown preset %q", path, l.PresetId)
		}
	}
	return &s, nil
}
