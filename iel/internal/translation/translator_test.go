package translation

import (
	"slices"
	"testing"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
)

func validator(t *testing.T) Translator {
	t.Helper()
	cat, err := ssot.LoadCatalog("../../db/catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Two pinned caption nodes from the TED.
	return Translator{Catalog: cat.WithNodes([]domain.Node{
		{ID: "cap-1", Label: "Caption: Logo", Role: domain.RoleVVF, VVFID: "vvf-caption"},
		{ID: "cap-2", Label: "Caption: Score", Role: domain.RoleVVF, VVFID: "vvf-caption"},
		{ID: "ctc-1", Role: domain.RoleVVF, VVFID: "vvf-color-transcode-crop"},
	})}
}

var defaults = map[apitypes.FunctionType]map[string]any{
	apitypes.FunctionTypeCaption:   {"text": "LIVE", "position": "bottom"},
	apitypes.FunctionTypeTranscode: {},
	apitypes.FunctionTypeCrop:      {"x": 0.0, "y": 0.0},
	apitypes.FunctionTypeColor:     {"r": 0.0, "g": 0.0, "b": 0.0},
}

// chain builds a chain from types with default params and ids sf-<type>-<n>.
func chain(types ...apitypes.FunctionType) []apitypes.FunctionInstance {
	out := make([]apitypes.FunctionInstance, len(types))
	for i, ty := range types {
		out[i] = apitypes.FunctionInstance{InstanceId: string(ty) + "-" + string(rune('1'+i)), Type: ty, Params: defaults[ty]}
		if ty == caption {
			out[i].VvfNodeId = ptr("cap-1")
		}
	}
	return out
}

func codes(issues []apitypes.ChainIssue) []apitypes.ChainIssueCode {
	out := make([]apitypes.ChainIssueCode, len(issues))
	for i, is := range issues {
		out[i] = is.Code
	}
	return out
}

const (
	caption   = apitypes.FunctionTypeCaption
	transcode = apitypes.FunctionTypeTranscode
	crop      = apitypes.FunctionTypeCrop
	color     = apitypes.FunctionTypeColor
)

func TestChain(t *testing.T) {
	v := validator(t)
	cases := []struct {
		name  string
		chain []apitypes.FunctionInstance
		want  []apitypes.ChainIssueCode
	}{
		{"caption then the bundle", chain(caption, transcode, crop, color), nil},
		{"bundle in any order", chain(color, crop, transcode), nil},
		{"empty chain keeps 8K DG", nil, []apitypes.ChainIssueCode{apitypes.ChainIssueCodeFORMATMISMATCH}},
		{"lone color", chain(color), []apitypes.ChainIssueCode{apitypes.ChainIssueCodeVVFINCOMPLETE, apitypes.ChainIssueCodeFORMATMISMATCH}},
		{"bundle split by a caption", chain(transcode, caption, crop, color),
			[]apitypes.ChainIssueCode{apitypes.ChainIssueCodeVVFNOTCONTIGUOUS, apitypes.ChainIssueCodeFORMATMISMATCH}},
		{"caption after transcode (8K FULL)", chain(transcode, crop, color, caption), []apitypes.ChainIssueCode{apitypes.ChainIssueCodeFORMATMISMATCH}},
		{"unknown function", []apitypes.FunctionInstance{{InstanceId: "d", Type: apitypes.FunctionTypeDelay, Params: map[string]any{}}},
			[]apitypes.ChainIssueCode{apitypes.ChainIssueCodeFUNCTIONUNAVAILABLE, apitypes.ChainIssueCodeFORMATMISMATCH}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues, _ := v.CheckChain(tc.chain)
			if got := codes(issues); !slices.Equal(got, tc.want) {
				t.Fatalf("issues = %v (%+v), want %v", got, issues, tc.want)
			}
		})
	}
}

func TestChainDuplicateAndParams(t *testing.T) {
	v := validator(t)
	c := chain(transcode, crop, color)
	c[1].InstanceId = c[0].InstanceId
	c[2].Params = map[string]any{"r": 1100.0, "g": 1.5, "b": 0.0, "z": 1.0}
	issues, _ := v.CheckChain(c)
	got := codes(issues)
	want := []apitypes.ChainIssueCode{apitypes.ChainIssueCodeDUPLICATEINSTANCEID,
		apitypes.ChainIssueCodeINVALIDPARAMS, apitypes.ChainIssueCodeINVALIDPARAMS, apitypes.ChainIssueCodeINVALIDPARAMS}
	if !slices.Equal(got, want) {
		t.Fatalf("issues = %v (%+v)", got, issues)
	}
}

func TestParams(t *testing.T) {
	v := validator(t)
	errs := v.CheckInterface(caption, map[string]any{"text": "a very long caption that goes on and on and on and on beyond sixty-four", "position": "top"})
	if len(errs) != 1 || *errs[0].Field != "text" {
		t.Fatalf("errors = %+v", errs)
	}
	errs = v.CheckInterface(caption, map[string]any{"text": "ok", "position": "left"})
	if len(errs) != 1 || errs[0].Message != "position must be one of top, bottom" {
		t.Fatalf("errors = %+v", errs)
	}
	if errs := v.CheckInterface(crop, map[string]any{"x": 5760.0}); len(errs) != 1 || *errs[0].Field != "y" {
		t.Fatalf("errors = %+v", errs)
	}
}

// SF instances group VVF functions and sort an unordered VVF into the End.AN execution order.
func TestSFInstances(t *testing.T) {
	v := validator(t)
	blocks := v.SFInstances(chain(caption, color, crop, transcode, caption))
	if len(blocks) != 3 {
		t.Fatalf("blocks = %+v", blocks)
	}
	var order []apitypes.FunctionType
	for _, fn := range blocks[1].Functions {
		order = append(order, fn.Type)
	}
	if blocks[1].VVF.Id != "vvf-color-transcode-crop" || !slices.Equal(order, []apitypes.FunctionType{transcode, crop, color}) {
		t.Fatalf("bundle = %s %v", blocks[1].VVF.Id, order)
	}
}

func ptr[T any](v T) *T { return &v }

func TestPinnedNodes(t *testing.T) {
	v := validator(t)
	bare := chain(caption, transcode, crop, color)
	bare[0].VvfNodeId = nil
	wrong := chain(caption, transcode, crop, color)
	wrong[0].VvfNodeId = ptr("ctc-1")
	named := chain(caption, transcode, crop, color)
	named[1].VvfNodeId = ptr("ctc-1")
	for name, tc := range map[string]struct {
		chain []apitypes.FunctionInstance
		want  apitypes.ChainIssueCode
	}{
		"pinned without a node":  {bare, apitypes.ChainIssueCodeVVFNODEREQUIRED},
		"pinned on another node": {wrong, apitypes.ChainIssueCodeVVFNODEUNKNOWN},
		"dynamic with a node":    {named, apitypes.ChainIssueCodeVVFNODEUNKNOWN},
	} {
		issues, _ := v.CheckChain(tc.chain)
		if got := codes(issues); !slices.Equal(got, []apitypes.ChainIssueCode{tc.want}) {
			t.Errorf("%s: issues = %v", name, got)
		}
	}
	// Captions on different nodes are separate instances, each on its node.
	two := chain(caption, caption, transcode, crop, color)
	two[1].VvfNodeId = ptr("cap-2")
	if issues, _ := v.CheckChain(two); len(issues) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
	blocks := v.SFInstances(two)
	if len(blocks) != 3 || blocks[0].Pinned != "cap-1" || blocks[1].Pinned != "cap-2" || blocks[2].Pinned != "" {
		t.Fatalf("blocks = %+v", blocks)
	}
	if n := *v.Catalog.Constraints.Vvfs[0].Nodes; len(n) != 2 || n[1].Label != "Caption: Score" {
		t.Fatalf("pinned nodes = %+v", n)
	}
	if n := *v.Catalog.Constraints.Vvfs[1].Nodes; len(n) != 0 {
		t.Fatalf("dynamic VVF lists nodes: %+v", n)
	}
}
