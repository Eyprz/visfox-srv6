// Package ssot holds the IEL's single source of truth: the static catalog and seeds, the resources
// derived from the TED, and the SQLite persistence of presets, layouts, matrix cells and settings.
package ssot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"go.yaml.in/yaml/v3"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
)

// Catalog is the VVF catalog and domain policy (iel/db/catalog.yaml): the chain rules served by
// GET /presets/constraints and the function catalog served by GET /functions.
type Catalog struct {
	Constraints apitypes.ChainConstraints      `json:"constraints"`
	Functions   []apitypes.FunctionCatalogItem `json:"functions"`
}

// LoadCatalog reads and checks the catalog file.
func LoadCatalog(path string) (*Catalog, error) {
	var c Catalog
	if err := decodeYAML(path, &c); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	if err := c.check(); err != nil {
		return nil, fmt.Errorf("catalog %s: %w", path, err)
	}
	// The API always states the behavior (process when the catalog omits it).
	for i := range c.Constraints.Vvfs {
		if c.Constraints.Vvfs[i].Behavior == nil {
			b := apitypes.VvfDefinitionBehaviorProcess
			c.Constraints.Vvfs[i].Behavior = &b
		}
	}
	return &c, nil
}

func (c *Catalog) check() error {
	vvfs := map[string]bool{}
	for _, v := range c.Constraints.Vvfs {
		if vvfs[v.Id] {
			return fmt.Errorf("duplicate VVF %s", v.Id)
		}
		vvfs[v.Id] = true
		if !v.Allocation.Valid() {
			return fmt.Errorf("VVF %s: allocation must be dynamic or pinned, not %q", v.Id, v.Allocation)
		}
		if v.Nodes != nil {
			return fmt.Errorf("VVF %s: nodes come from the TED, not from the catalog", v.Id)
		}
		if v.Behavior != nil && !v.Behavior.Valid() {
			return fmt.Errorf("VVF %s: behavior must be process, select or replicate, not %q", v.Id, *v.Behavior)
		}
		if v.Behavior != nil && *v.Behavior != apitypes.VvfDefinitionBehaviorProcess && len(v.FunctionTypes) > 0 {
			return fmt.Errorf("VVF %s: a %s VVF controls the stream flow and offers no functions", v.Id, *v.Behavior)
		}
	}
	seen := map[apitypes.FunctionType]bool{}
	for _, f := range c.Functions {
		if seen[f.Type] {
			return fmt.Errorf("duplicate function %s", f.Type)
		}
		seen[f.Type] = true
		if f.VvfId != nil && !vvfs[*f.VvfId] {
			return fmt.Errorf("function %s refers to unknown VVF %s", f.Type, *f.VvfId)
		}
	}
	for _, v := range c.Constraints.Vvfs {
		for _, t := range v.FunctionTypes {
			if !seen[t] {
				return fmt.Errorf("VVF %s offers %s, which is not in the function catalog", v.Id, t)
			}
		}
	}
	return nil
}

// WithNodes returns a copy whose pinned VVFs list their TED nodes (GET /presets/constraints), in the
// given order. Dynamic VVFs get an empty list: their nodes are assigned by the IEL.
func (c *Catalog) WithNodes(nodes []domain.Node) *Catalog {
	out := &Catalog{Constraints: c.Constraints, Functions: c.Functions}
	out.Constraints.Vvfs = make([]apitypes.VvfDefinition, len(c.Constraints.Vvfs))
	for i, v := range c.Constraints.Vvfs {
		refs := []apitypes.VvfNodeRef{}
		if v.Allocation == apitypes.VvfDefinitionAllocationPinned {
			for _, n := range nodes {
				if n.VVFID == v.Id {
					refs = append(refs, apitypes.VvfNodeRef{NodeId: n.ID, Label: n.Label})
				}
			}
		}
		v.Nodes = &refs
		out.Constraints.Vvfs[i] = v
	}
	return out
}

// Function returns the catalog entry of a function type.
func (c *Catalog) Function(t apitypes.FunctionType) (apitypes.FunctionCatalogItem, bool) {
	i := slices.IndexFunc(c.Functions, func(f apitypes.FunctionCatalogItem) bool { return f.Type == t })
	if i < 0 {
		return apitypes.FunctionCatalogItem{}, false
	}
	return c.Functions[i], true
}

// VVFOf returns the VVF definition that offers a function type.
func (c *Catalog) VVFOf(t apitypes.FunctionType) (apitypes.VvfDefinition, bool) {
	for _, v := range c.Constraints.Vvfs {
		if slices.Contains(v.FunctionTypes, t) {
			return v, true
		}
	}
	return apitypes.VvfDefinition{}, false
}

// VVF returns a VVF definition by id.
func (c *Catalog) VVF(id string) (apitypes.VvfDefinition, bool) {
	i := slices.IndexFunc(c.Constraints.Vvfs, func(v apitypes.VvfDefinition) bool { return v.Id == id })
	if i < 0 {
		return apitypes.VvfDefinition{}, false
	}
	return c.Constraints.Vvfs[i], true
}

// decodeYAML reads a YAML file into v through JSON, so the generated API models (JSON tags only)
// can be filled directly. Unknown fields are rejected.
func decodeYAML(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	j, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("convert %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(j))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
