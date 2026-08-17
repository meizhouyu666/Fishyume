package contextcompiler

import (
	"fmt"
	"sort"
	"unicode/utf8"

	"wf.local/wf-engine/internal/agent"
)

// ContextCompilerInputV2 contains the complete, already-resolved input to the
// pure compiler. ExecutionContract and OutputContract are engine-owned values;
// source resolution is never allowed to supply either kind.
type ContextCompilerInputV2 struct {
	Identity          agent.AttemptIdentity     `json:"identity"`
	Resolution        ContextSourceResolutionV2 `json:"resolution"`
	ExecutionContract ContextComponentV2        `json:"executionContract"`
	OutputContract    ContextComponentV2        `json:"outputContract"`
	Budget            AttentionBudgetV2         `json:"budget"`
}

type CompilationV2 struct {
	Envelope ContextEnvelopeV2 `json:"envelope"`
	Manifest ContextManifestV2 `json:"manifest"`
	Hash     string            `json:"hash"`
}

// Compatibility aliases make the additive v2 surface easy to discover while
// leaving the production v1 Compile function untouched.
type ContextCompilationInputV2 = ContextCompilerInputV2
type ContextCompilationV2 = CompilationV2

// CompileContextV2 compiles approved source resolution and engine contracts
// without filesystem, clock, model, Provider, or persistence access.
func CompileContextV2(input ContextCompilerInputV2) (CompilationV2, error) {
	if err := validateBudgetV2(input.Budget); err != nil {
		return CompilationV2{}, err
	}
	if stringsTrim(input.Identity.RunID) == "" || stringsTrim(input.Identity.NodeID) == "" || input.Identity.Attempt < 1 {
		return CompilationV2{}, contractError(CodeContextInvalidComponent, "Attempt identity is incomplete", "")
	}
	if len(input.Resolution.Components) > MaxContextComponents || len(input.Resolution.Omissions) > MaxContextOmissions {
		return CompilationV2{}, contractError(CodeContextInvalidComponent, "Context source count exceeds its bound", "")
	}
	// Validate and copy source data before doing any allocation. This also
	// rejects spoofed engine-owned kinds and included/omitted identity overlap.
	sources := make([]ContextComponentV2, len(input.Resolution.Components))
	copy(sources, input.Resolution.Components)
	omissions := make([]ContextOmissionV2, len(input.Resolution.Omissions))
	copy(omissions, input.Resolution.Omissions)
	seen := make(map[string]struct{}, len(sources)+len(omissions)+2)
	for _, component := range sources {
		if component.Kind == KindExecutionContract || component.Kind == KindOutputContract {
			return CompilationV2{}, contractError(CodeContextInvalidComponent, "source resolution cannot provide engine-owned contracts", component.ID)
		}
		if err := validateComponentV2(component); err != nil {
			return CompilationV2{}, err
		}
		if component.Truncation != TruncationNone || component.OriginalBytes != component.IncludedBytes {
			return CompilationV2{}, contractError(CodeContextInvalidComponent, "source resolution components must be complete and untruncated", component.ID)
		}
		if _, ok := seen[component.ID]; ok {
			return CompilationV2{}, contractError(CodeContextInvalidComponent, "duplicate Context component ID", component.ID)
		}
		seen[component.ID] = struct{}{}
	}
	for _, omission := range omissions {
		if err := validateOmissionV2(omission); err != nil {
			return CompilationV2{}, err
		}
		if _, ok := seen[omission.ComponentID]; ok {
			return CompilationV2{}, contractError(CodeContextInvalidComponent, "included and omitted component IDs overlap", omission.ComponentID)
		}
		seen[omission.ComponentID] = struct{}{}
	}
	if err := validateEngineContract(input.ExecutionContract, KindExecutionContract); err != nil {
		return CompilationV2{}, err
	}
	if err := validateEngineContract(input.OutputContract, KindOutputContract); err != nil {
		return CompilationV2{}, err
	}
	if _, ok := seen[input.ExecutionContract.ID]; ok {
		return CompilationV2{}, contractError(CodeContextInvalidComponent, "engine contract ID overlaps source resolution", input.ExecutionContract.ID)
	}
	if _, ok := seen[input.OutputContract.ID]; ok || input.OutputContract.ID == input.ExecutionContract.ID {
		return CompilationV2{}, contractError(CodeContextInvalidComponent, "engine contract IDs overlap source resolution", input.OutputContract.ID)
	}
	sources = append(sources, input.ExecutionContract, input.OutputContract)
	components, budgetOmissions, err := allocateV2(sources, input.Budget)
	if err != nil {
		return CompilationV2{}, err
	}
	omissions = append(omissions, budgetOmissions...)
	sort.Slice(omissions, func(i, j int) bool { return omissions[i].ComponentID < omissions[j].ComponentID })
	if len(omissions) > MaxContextOmissions {
		return CompilationV2{}, contractError(CodeContextInvalidComponent, "Context omission count exceeds its bound", "")
	}
	envelope := ContextEnvelopeV2{SchemaVersion: EnvelopeV2Version, CompilerVersion: CompilerV2Version, Identity: input.Identity, Budget: input.Budget, Components: components, Omissions: omissions}
	if err := ValidateContextEnvelopeV2(envelope); err != nil {
		return CompilationV2{}, err
	}
	hash, err := CanonicalEnvelopeHashV2(envelope)
	if err != nil {
		return CompilationV2{}, err
	}
	manifest, err := BuildContextManifestV2(envelope)
	if err != nil {
		return CompilationV2{}, err
	}
	return CompilationV2{Envelope: envelope, Manifest: manifest, Hash: hash}, nil
}

// CompileV2 is a concise compatibility alias.
func CompileV2(input ContextCompilerInputV2) (CompilationV2, error) { return CompileContextV2(input) }

func CompileAttentionBudgetV2(input ContextCompilerInputV2) (CompilationV2, error) {
	return CompileContextV2(input)
}

func validateEngineContract(component ContextComponentV2, kind ComponentKind) error {
	if component.Kind != kind || component.Tier != TierRequired {
		return contractError(CodeContextInvalidComponent, fmt.Sprintf("engine-owned %s contract must be required", kind), component.ID)
	}
	if !utf8.ValidString(component.Content) {
		return contractError(CodeContextInvalidComponent, "engine contract content is not valid UTF-8", component.ID)
	}
	return validateComponentV2(component)
}

func allocateV2(sources []ContextComponentV2, budget AttentionBudgetV2) ([]ContextComponentV2, []ContextOmissionV2, error) {
	required, important, optional := make([]ContextComponentV2, 0), make([]ContextComponentV2, 0), make([]ContextComponentV2, 0)
	for _, c := range sources {
		switch c.Tier {
		case TierRequired:
			required = append(required, c)
		case TierImportant:
			important = append(important, c)
		case TierOptional:
			optional = append(optional, c)
		}
	}
	canonicalComponents := func(a, b ContextComponentV2) bool {
		r1, r2 := componentRank(a.Kind), componentRank(b.Kind)
		if r1 != r2 {
			return r1 < r2
		}
		return a.ID < b.ID
	}
	sort.Slice(required, func(i, j int) bool { return canonicalComponents(required[i], required[j]) })
	sort.Slice(important, func(i, j int) bool { return canonicalComponents(important[i], important[j]) })
	sort.Slice(sources, func(i, j int) bool {
		r1, r2 := componentRank(sources[i].Kind), componentRank(sources[j].Kind)
		if r1 != r2 {
			return r1 < r2
		}
		return sources[i].ID < sources[j].ID
	})
	if sumBytes(required) > budget.RequiredBytes {
		return nil, nil, contractError(CodeContextBudgetUnsatisfiable, "required Context exceeds required tier budget", "")
	}
	result := make([]ContextComponentV2, 0, len(sources))
	result = append(result, required...)
	omissions := make([]ContextOmissionV2, 0)
	impAlloc := balancedAlloc(important, budget.ImportantBytes)
	for i, c := range important {
		n := impAlloc[i]
		if n == 0 {
			continue
		}
		if n < len([]byte(c.Content)) {
			c.Content = validTail(c.Content, n)
			if c.Content == "" {
				omissions = append(omissions, omissionFor(c))
				continue
			}
			c.IncludedBytes = len([]byte(c.Content))
			c.ContentHash = hashBytes([]byte(c.Content))
			c.Truncation = TruncationTail
		}
		result = append(result, c)
	}
	for i, c := range important {
		if impAlloc[i] == 0 {
			omissions = append(omissions, omissionFor(c))
		}
	}
	// Optional Memory uses whole-record semantics and cannot displace other tiers.
	remaining := budget.OptionalBytes
	sort.Slice(optional, func(i, j int) bool { return optional[i].ID < optional[j].ID })
	for _, c := range optional {
		size := len([]byte(c.Content))
		if size <= remaining {
			result = append(result, c)
			remaining -= size
		} else {
			omissions = append(omissions, omissionFor(c))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		r1, r2 := componentRank(result[i].Kind), componentRank(result[j].Kind)
		if r1 != r2 {
			return r1 < r2
		}
		return result[i].ID < result[j].ID
	})
	sort.Slice(omissions, func(i, j int) bool { return omissions[i].ComponentID < omissions[j].ComponentID })
	return result, omissions, nil
}

func omissionFor(c ContextComponentV2) ContextOmissionV2 {
	return ContextOmissionV2{ComponentID: c.ID, Kind: c.Kind, Tier: c.Tier, Reason: OmissionBudgetExhausted, SourceHash: c.Provenance.SourceHash, OriginalBytes: c.OriginalBytes}
}
func sumBytes(cs []ContextComponentV2) int {
	n := 0
	for _, c := range cs {
		n += len([]byte(c.Content))
	}
	return n
}

// balancedAlloc computes a max-min (water-filling) allocation. Equal-size
// components are tie-broken by canonical component ID order.
func balancedAlloc(cs []ContextComponentV2, budget int) []int {
	a := make([]int, len(cs))
	if len(cs) == 0 || budget <= 0 {
		return a
	}
	sizes := make([]int, len(cs))
	max := 0
	for i, c := range cs {
		sizes[i] = len([]byte(c.Content))
		if sizes[i] > max {
			max = sizes[i]
		}
	}
	lo, hi := 0, max
	for lo < hi {
		mid := (lo + hi + 1) / 2
		total := 0
		for _, s := range sizes {
			if s < mid {
				total += s
			} else {
				total += mid
			}
			if total > budget {
				break
			}
		}
		if total <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	level := lo
	used := 0
	for i, s := range sizes {
		if s < level {
			a[i] = s
		} else {
			a[i] = level
		}
		used += a[i]
	}
	left := budget - used
	for left > 0 {
		active := 0
		for i, s := range sizes {
			if a[i] < s {
				active++
			}
		}
		if active == 0 {
			break
		}
		share, remainder := left/active, left%active
		if share == 0 {
			for i, s := range sizes {
				if left == 0 {
					break
				}
				if a[i] < s {
					a[i]++
					left--
				}
			}
			continue
		}
		before := left
		seen := 0
		for i, s := range sizes {
			if a[i] >= s {
				continue
			}
			add := share
			if seen < remainder {
				add++
			}
			if cap := s - a[i]; add > cap {
				add = cap
			}
			a[i] += add
			left -= add
			seen++
		}
		if left == before {
			break
		}
	}
	return a
}

func validTail(content string, maxBytes int) string {
	b := []byte(content)
	if maxBytes >= len(b) {
		return content
	}
	if maxBytes <= 0 {
		return ""
	}
	start := len(b) - maxBytes
	for start < len(b) && !utf8.RuneStart(b[start]) {
		start++
	}
	return string(b[start:])
}

func stringsTrim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r' || s[len(s)-1] == '\n') {
		s = s[:len(s)-1]
	}
	return s
}
