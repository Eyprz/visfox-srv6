// Package translation is Intent Translation (RFC 9315 §5.1.2): it checks what the user asked for
// (Interface Validator: function params against their schema; SFC Validator, logical part: chain
// rules, VVF composition and formats against the VVF catalog and the domain policy) and turns a
// crosspoint intent into an SFC request of SF instances for Intent Orchestration.
// The rules follow the MSW mock IEL (application/src/mocks/db.ts),
// which stays the reference implementation.
package translation

import (
	"fmt"
	"math"
	"slices"
	"strings"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ssot"
)

// Translator checks intents against the catalog and translates them into SFC requests.
type Translator struct {
	Catalog *ssot.Catalog
}

// CheckInterface is the Interface Validator: it checks function params against the catalog's
// param_schema (required, known keys, types, ranges, string length and enums).
func (v Translator) CheckInterface(t apitypes.FunctionType, params map[string]any) []apitypes.FieldError {
	item, ok := v.Catalog.Function(t)
	if !ok {
		return []apitypes.FieldError{{Message: fmt.Sprintf("Unknown function type: %s", t)}}
	}
	var errs []apitypes.FieldError
	fail := func(field, format string, args ...any) {
		errs = append(errs, apitypes.FieldError{Field: &field, Message: fmt.Sprintf(format, args...)})
	}
	for _, key := range stringList(item.ParamSchema["required"]) {
		if _, ok := params[key]; !ok {
			fail(key, "%s is required", key)
		}
	}
	props, _ := item.ParamSchema["properties"].(map[string]any)
	for _, key := range sortedKeys(params) {
		value := params[key]
		rule, ok := props[key].(map[string]any)
		if !ok {
			fail(key, "%s is not a known parameter", key)
			continue
		}
		switch rule["type"] {
		case "integer", "number":
			n, isNum := number(value)
			if !isNum || (rule["type"] == "integer" && n != math.Trunc(n)) {
				kind := "a number"
				if rule["type"] == "integer" {
					kind = "an integer"
				}
				fail(key, "%s must be %s", key, kind)
				continue
			}
			lo, hasLo := number(rule["minimum"])
			hi, hasHi := number(rule["maximum"])
			if (hasLo && n < lo) || (hasHi && n > hi) {
				fail(key, "%s must be between %v and %v", key, rule["minimum"], rule["maximum"])
			}
		case "string":
			s, isStr := value.(string)
			if !isStr {
				fail(key, "%s must be a string", key)
				continue
			}
			if max, ok := number(rule["maxLength"]); ok && float64(len([]rune(s))) > max {
				fail(key, "%s must be at most %v characters", key, rule["maxLength"])
			} else if enum := stringList(rule["enum"]); len(enum) > 0 && !slices.Contains(enum, s) {
				fail(key, "%s must be one of %s", key, strings.Join(enum, ", "))
			}
		}
	}
	return errs
}

func formatLabel(f apitypes.VideoFormat) string {
	switch f {
	case apitypes.VideoFormatN8kFull:
		return "8K FULL"
	case apitypes.VideoFormatN8kDg:
		return "8K DG"
	}
	return string(f)
}

// Propagate passes a media format through the chain using the catalog's format_transitions. It
// returns the output format, or an issue naming the first function that cannot take its input.
func (v Translator) Propagate(in apitypes.VideoFormat, chain []apitypes.FunctionInstance) (apitypes.VideoFormat, *apitypes.ChainIssue) {
	current := in
	for _, fn := range chain {
		item, _ := v.Catalog.Function(fn.Type)
		if item.FormatTransitions == nil || item.FormatTransitions.VideoFormat == nil {
			continue
		}
		next, ok := (*item.FormatTransitions.VideoFormat)[string(current)]
		if !ok {
			id := fn.InstanceId
			return "", &apitypes.ChainIssue{
				Code: apitypes.ChainIssueCodeFORMATMISMATCH, InstanceId: &id,
				Message: fmt.Sprintf("%s cannot take %s as input", fn.InstanceId, formatLabel(current)),
			}
		}
		current = next
	}
	return current, nil
}

// CheckFormat reports why the chain cannot turn in into out (nil: it can).
func (v Translator) CheckFormat(in, out apitypes.VideoFormat, chain []apitypes.FunctionInstance) *apitypes.ChainIssue {
	got, issue := v.Propagate(in, chain)
	if issue != nil {
		return issue
	}
	if got != out {
		return &apitypes.ChainIssue{
			Code:    apitypes.ChainIssueCodeFORMATMISMATCH,
			Message: fmt.Sprintf("Output must be %s, but the chain outputs %s", formatLabel(out), formatLabel(got)),
		}
	}
	return nil
}

// group is a VVF instance while the chain is grouped (chain order, start index kept).
type group struct {
	VVF       apitypes.VvfDefinition
	Pinned    string
	Instances []apitypes.FunctionInstance
	start     int
}

func nodeOf(fn apitypes.FunctionInstance) string {
	if fn.VvfNodeId == nil {
		return ""
	}
	return *fn.VvfNodeId
}

// SFInstances groups a chain into VVF instances (what Intent Orchestration places). Functions of an
// unordered VVF are sorted into the VVF's execution order.
func (v Translator) SFInstances(chain []apitypes.FunctionInstance) []domain.SFInstance {
	groups := v.instances(chain)
	out := make([]domain.SFInstance, len(groups))
	for i, g := range groups {
		order := g.VVF.FunctionTypes
		fns := slices.Clone(g.Instances)
		slices.SortStableFunc(fns, func(a, b apitypes.FunctionInstance) int {
			return slices.Index(order, a.Type) - slices.Index(order, b.Type)
		})
		out[i] = domain.SFInstance{VVF: g.VVF, Pinned: g.Pinned, Functions: fns}
	}
	return out
}

// Translate turns a crosspoint intent into the SFC request Intent Orchestration realizes.
func (v Translator) Translate(in domain.Intent) domain.SFCRequest {
	return domain.SFCRequest{IntentID: in.ID, SourceID: in.SourceID, DestinationID: in.DestinationID,
		SFs: v.SFInstances(in.Chain), OnAir: in.OnAir}
}

// instances groups the chain in chain order (no sorting).
func (v Translator) instances(chain []apitypes.FunctionInstance) []group {
	var blocks []group
	for i, fn := range chain {
		vvf, ok := v.Catalog.VVFOf(fn.Type)
		if !ok {
			continue
		}
		if n := len(blocks); n > 0 {
			last := &blocks[n-1]
			adjacent := last.start+len(last.Instances) == i
			samePin := vvf.Allocation != apitypes.VvfDefinitionAllocationPinned || nodeOf(last.Instances[0]) == nodeOf(fn)
			if adjacent && last.VVF.Id == vvf.Id && samePin &&
				!slices.ContainsFunc(last.Instances, sameType(fn.Type)) {
				last.Instances = append(last.Instances, fn)
				continue
			}
		}
		b := group{VVF: vvf, Instances: []apitypes.FunctionInstance{fn}, start: i}
		if vvf.Allocation == apitypes.VvfDefinitionAllocationPinned {
			b.Pinned = nodeOf(fn)
		}
		blocks = append(blocks, b)
	}
	return blocks
}

func sameType(t apitypes.FunctionType) func(apitypes.FunctionInstance) bool {
	return func(f apitypes.FunctionInstance) bool { return f.Type == t }
}

// nodeIssues checks the node of every function: a pinned VVF's function names one of the VVF's nodes,
// a dynamic VVF's function names none (the IEL assigns it).
func (v Translator) nodeIssues(chain []apitypes.FunctionInstance) []apitypes.ChainIssue {
	var issues []apitypes.ChainIssue
	for _, fn := range chain {
		vvf, ok := v.Catalog.VVFOf(fn.Type)
		if !ok {
			continue
		}
		id, inst, node := vvf.Id, fn.InstanceId, nodeOf(fn)
		pinned := vvf.Allocation == apitypes.VvfDefinitionAllocationPinned
		switch {
		case pinned && node == "":
			issues = append(issues, apitypes.ChainIssue{
				Code: apitypes.ChainIssueCodeVVFNODEREQUIRED, VvfId: &id, InstanceId: &inst,
				Message: fmt.Sprintf("Choose which '%s' node %s uses", vvf.Name, inst),
			})
		case pinned && !slices.ContainsFunc(nodeRefs(vvf), func(r apitypes.VvfNodeRef) bool { return r.NodeId == node }):
			issues = append(issues, apitypes.ChainIssue{
				Code: apitypes.ChainIssueCodeVVFNODEUNKNOWN, VvfId: &id, InstanceId: &inst,
				Message: fmt.Sprintf("%s is not a '%s' node", node, vvf.Name),
			})
		case !pinned && node != "":
			issues = append(issues, apitypes.ChainIssue{
				Code: apitypes.ChainIssueCodeVVFNODEUNKNOWN, VvfId: &id, InstanceId: &inst,
				Message: fmt.Sprintf("'%s' nodes are assigned by the IEL; %s must not name one", vvf.Name, inst),
			})
		}
	}
	return issues
}

func nodeRefs(v apitypes.VvfDefinition) []apitypes.VvfNodeRef {
	if v.Nodes == nil {
		return nil
	}
	return *v.Nodes
}

// vvfIssues checks the VVF rules per VVF instance: every instance of a require_all VVF needs all its
// functions, members split by other functions are not contiguous, and ordered VVFs keep their order.
func (v Translator) vvfIssues(chain []apitypes.FunctionInstance) []apitypes.ChainIssue {
	insts := v.instances(chain)
	var issues []apitypes.ChainIssue
	notContiguous := map[string]bool{}
	for i, inst := range insts {
		vvf := inst.VVF
		var missing []string
		for _, t := range vvf.FunctionTypes {
			if !slices.ContainsFunc(inst.Instances, sameType(t)) {
				missing = append(missing, string(t))
			}
		}
		split := false
		if vvf.Contiguous && len(vvf.FunctionTypes) > 1 {
			for j, o := range insts {
				if j == i || o.VVF.Id != vvf.Id {
					continue
				}
				overlap := slices.ContainsFunc(o.Instances, func(f apitypes.FunctionInstance) bool {
					return slices.ContainsFunc(inst.Instances, sameType(f.Type))
				})
				completes := slices.ContainsFunc(o.Instances, func(f apitypes.FunctionInstance) bool {
					return slices.Contains(missing, string(f.Type))
				})
				if !overlap && completes {
					split = true
					break
				}
			}
		}
		id, first := vvf.Id, inst.Instances[0].InstanceId
		switch {
		case split:
			if !notContiguous[vvf.Id] {
				issues = append(issues, apitypes.ChainIssue{
					Code: apitypes.ChainIssueCodeVVFNOTCONTIGUOUS, VvfId: &id,
					Message: fmt.Sprintf("Functions of VVF '%s' must be adjacent", vvf.Name),
				})
			}
			notContiguous[vvf.Id] = true
		case vvf.RequireAll && len(missing) > 0:
			issues = append(issues, apitypes.ChainIssue{
				Code: apitypes.ChainIssueCodeVVFINCOMPLETE, VvfId: &id, InstanceId: &first,
				Message: fmt.Sprintf("This '%s' VVF also requires %s (each VVF instance needs all of its functions)",
					vvf.Name, strings.Join(missing, ", ")),
			})
		}
		if vvf.Ordered {
			prev := -1
			for _, f := range inst.Instances {
				idx := slices.Index(vvf.FunctionTypes, f.Type)
				if idx < prev {
					order := make([]string, len(vvf.FunctionTypes))
					for k, t := range vvf.FunctionTypes {
						order[k] = string(t)
					}
					issues = append(issues, apitypes.ChainIssue{
						Code: apitypes.ChainIssueCodeVVFORDER, VvfId: &id, InstanceId: &first,
						Message: fmt.Sprintf("Functions of VVF '%s' must follow %s", vvf.Name, strings.Join(order, " > ")),
					})
					break
				}
				prev = idx
			}
		}
	}
	return issues
}

// CheckChain is the logical part of the SFC Validator: it checks a chain against the chain rules,
// the VVF composition and the domain formats. It returns the issues and the output format for the
// domain's source format (nil when the format cannot propagate).
func (v Translator) CheckChain(chain []apitypes.FunctionInstance) ([]apitypes.ChainIssue, *apitypes.VideoFormat) {
	c := v.Catalog.Constraints
	issues := []apitypes.ChainIssue{}
	if len(chain) < c.MinFunctions {
		issues = append(issues, apitypes.ChainIssue{
			Code: apitypes.ChainIssueCodeEMPTYCHAIN, Message: fmt.Sprintf("At least %d functions are required", c.MinFunctions),
		})
	}
	if c.MaxFunctions != nil && len(chain) > *c.MaxFunctions {
		issues = append(issues, apitypes.ChainIssue{
			Code: apitypes.ChainIssueCodeMAXFUNCTIONSEXCEEDED, Message: fmt.Sprintf("At most %d functions are allowed", *c.MaxFunctions),
		})
	}
	ids := map[string]bool{}
	counts := map[apitypes.FunctionType]int{}
	var order []apitypes.FunctionType
	for _, fn := range chain {
		id := fn.InstanceId
		if ids[id] {
			issues = append(issues, apitypes.ChainIssue{
				Code: apitypes.ChainIssueCodeDUPLICATEINSTANCEID, InstanceId: &id, Message: id + " is duplicated",
			})
		}
		ids[id] = true
		if counts[fn.Type] == 0 {
			order = append(order, fn.Type)
		}
		counts[fn.Type]++
		if _, ok := v.Catalog.Function(fn.Type); !ok {
			issues = append(issues, apitypes.ChainIssue{
				Code: apitypes.ChainIssueCodeFUNCTIONUNAVAILABLE, InstanceId: &id,
				Message: fmt.Sprintf("%s is not offered by any VVF", fn.Type),
			})
			continue
		}
		for _, e := range v.CheckInterface(fn.Type, fn.Params) {
			issues = append(issues, apitypes.ChainIssue{Code: apitypes.ChainIssueCodeINVALIDPARAMS, InstanceId: &id, Message: e.Message})
		}
	}
	if c.MaxInstancesPerType != nil {
		for _, t := range order {
			if max, ok := (*c.MaxInstancesPerType)[string(t)]; ok && counts[t] > max {
				issues = append(issues, apitypes.ChainIssue{
					Code: apitypes.ChainIssueCodeTYPELIMITEXCEEDED, Message: fmt.Sprintf("At most %d %s per chain", max, t),
				})
			}
		}
	}
	issues = append(issues, v.nodeIssues(chain)...)
	issues = append(issues, v.vvfIssues(chain)...)

	// Formats are checked against the domain policy: IN / OUT are not known while designing.
	in, out := c.Domain.SourceFormat.VideoFormat, c.Domain.DestinationFormat.VideoFormat
	if issue := v.CheckFormat(in, out, chain); issue != nil {
		issues = append(issues, *issue)
	}
	got, issue := v.Propagate(in, chain)
	if issue != nil {
		return issues, nil
	}
	return issues, &got
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func stringList(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
