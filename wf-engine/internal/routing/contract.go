package routing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// M6.0 freezes the routing vocabulary without connecting it to a Driver. The
// values are deliberately provider-neutral so a later catalog can describe
// Codex, Claude, or another external Agent process uniformly.
const (
	CapabilityCatalogV1Version  = "fishyume.capability-catalog/v1"
	RoutingRequirementV1Version = "fishyume.routing-requirement/v1"
	RoutingDecisionV1Version    = "fishyume.routing-decision/v1"
	RoutingUsageV1Version       = "fishyume.routing-usage/v1"
	PromptProfileV1Version      = "fishyume.prompt-profile/v1"
	RoutingPolicyV1Version      = "fishyume.routing-policy/v1"
	MaxCatalogModels            = 256
	MaxCandidates               = 32
	MaxFallbacks                = 8
	MaxReasonCodes              = 32
	MaxPromptComponents         = 32
	MaxRoutingBudgetBytes       = 16 * 1024 * 1024
	MaxCostUnits                = 1000000
)

type ErrorCode string

const (
	CodeInvalidContract      ErrorCode = "routing_invalid_contract"
	CodeUnsupportedVersion   ErrorCode = "routing_unsupported_version"
	CodeDuplicateIdentity    ErrorCode = "routing_duplicate_identity"
	CodeInvalidTarget        ErrorCode = "routing_invalid_target"
	CodeInvalidCapability    ErrorCode = "routing_invalid_capability"
	CodeInvalidBudget        ErrorCode = "routing_invalid_budget"
	CodeInvalidFallback      ErrorCode = "routing_invalid_fallback"
	CodeInvalidPromptProfile ErrorCode = "routing_invalid_prompt_profile"
	CodeCatalogHashMismatch  ErrorCode = "routing_catalog_hash_mismatch"
)

var StableErrorCodes = []ErrorCode{
	CodeInvalidContract, CodeUnsupportedVersion, CodeDuplicateIdentity,
	CodeInvalidTarget, CodeInvalidCapability, CodeInvalidBudget,
	CodeInvalidFallback, CodeInvalidPromptProfile, CodeCatalogHashMismatch,
}

type ContractError struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	SubjectID string    `json:"subjectId,omitempty"`
}

func (e *ContractError) Error() string {
	if e == nil {
		return ""
	}
	if e.SubjectID == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.SubjectID)
}

func contractError(code ErrorCode, message, subject string) error {
	return &ContractError{Code: code, Message: message, SubjectID: subject}
}

type Capability string

const (
	CapabilityRepoRead         Capability = "repo_read"
	CapabilityRepoEdit         Capability = "repo_edit"
	CapabilityToolUse          Capability = "tool_use"
	CapabilityStructuredOutput Capability = "structured_output"
	CapabilityStreaming        Capability = "streaming"
	CapabilityNeedsInput       Capability = "needs_input"
)

var StableCapabilities = []Capability{
	CapabilityRepoRead, CapabilityRepoEdit, CapabilityToolUse,
	CapabilityStructuredOutput, CapabilityStreaming, CapabilityNeedsInput,
}

type Complexity string

const (
	ComplexitySimple   Complexity = "simple"
	ComplexityStandard Complexity = "standard"
	ComplexityComplex  Complexity = "complex"
)

type QualityClass string

const (
	QualityEconomy  QualityClass = "economy"
	QualityBalanced QualityClass = "balanced"
	QualityPremium  QualityClass = "premium"
)

type CostClass string

const (
	CostLow    CostClass = "low"
	CostMedium CostClass = "medium"
	CostHigh   CostClass = "high"
)

type LatencyClass string

const (
	LatencyFast     LatencyClass = "fast"
	LatencyBalanced LatencyClass = "balanced"
	LatencySlow     LatencyClass = "slow"
)

// Target identifies the external process route. Model is not a Driver name;
// a Driver remains responsible for launching the selected Agent runtime.
type Target struct {
	Driver   string `json:"driver"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type ModelCapabilityV1 struct {
	ID                   string       `json:"id"`
	Target               Target       `json:"target"`
	Capabilities         []Capability `json:"capabilities"`
	ContextLimitBytes    int          `json:"contextLimitBytes"`
	MaxOutputBytes       int          `json:"maxOutputBytes"`
	Quality              QualityClass `json:"quality"`
	Cost                 CostClass    `json:"cost"`
	Latency              LatencyClass `json:"latency"`
	SupportsCancellation bool         `json:"supportsCancellation"`
}

type CapabilityCatalogV1 struct {
	SchemaVersion string              `json:"schemaVersion"`
	PolicyVersion string              `json:"policyVersion"`
	Models        []ModelCapabilityV1 `json:"models"`
}

type RoutingRequirementV1 struct {
	SchemaVersion      string       `json:"schemaVersion"`
	Capabilities       []Capability `json:"capabilities"`
	Complexity         Complexity   `json:"complexity"`
	Quality            QualityClass `json:"quality"`
	Latency            LatencyClass `json:"latency"`
	MaxCostUnits       int          `json:"maxCostUnits"`
	MaxContextBytes    int          `json:"maxContextBytes"`
	MaxOutputBytes     int          `json:"maxOutputBytes"`
	Candidates         []string     `json:"candidates,omitempty"`
	PromptProfile      string       `json:"promptProfile,omitempty"`
	AllowModelFallback bool         `json:"allowModelFallback"`
}

type BudgetGrantV1 struct {
	MaxCostUnits int `json:"maxCostUnits"`
	ContextBytes int `json:"contextBytes"`
	OutputBytes  int `json:"outputBytes"`
}

type FallbackMode string

const (
	FallbackNone     FallbackMode = "none"
	FallbackEligible FallbackMode = "eligible"
)

type FallbackPolicyV1 struct {
	Mode                FallbackMode `json:"mode"`
	MaxAttempts         int          `json:"maxAttempts"`
	RequireNoSideEffect bool         `json:"requireNoSideEffect"`
	RequireApproval     bool         `json:"requireApproval"`
}

type PromptComponentV1 struct {
	ID       string `json:"id"`
	Required bool   `json:"required"`
}

type PromptProfileV1 struct {
	SchemaVersion string              `json:"schemaVersion"`
	ID            string              `json:"id"`
	Version       string              `json:"version"`
	Components    []PromptComponentV1 `json:"components"`
	Description   string              `json:"description,omitempty"`
}

type RoutingDecisionV1 struct {
	SchemaVersion  string               `json:"schemaVersion"`
	CatalogHash    string               `json:"catalogHash"`
	Requirement    RoutingRequirementV1 `json:"requirement"`
	Selected       Target               `json:"selected"`
	ReasonCodes    []string             `json:"reasonCodes"`
	Budget         BudgetGrantV1        `json:"budget"`
	Fallback       []Target             `json:"fallback,omitempty"`
	FallbackPolicy FallbackPolicyV1     `json:"fallbackPolicy"`
	PromptProfile  string               `json:"promptProfile,omitempty"`
}

// RoutingUsageV1 is an immutable cost reservation captured with an Attempt.
// CostUnits use the trusted catalog's coarse cost class; they are not a
// Provider invoice or a token-price estimate.
type RoutingUsageV1 struct {
	SchemaVersion       string `json:"schemaVersion"`
	Target              Target `json:"target"`
	RouteIndex          int    `json:"routeIndex"`
	CostUnits           int    `json:"costUnits"`
	CumulativeCostUnits int    `json:"cumulativeCostUnits"`
}

var identityPattern = regexp.MustCompile(`^[a-z][a-z0-9._:/-]{0,127}$`)
var reasonPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

func ValidateCatalog(catalog CapabilityCatalogV1) error {
	if catalog.SchemaVersion != CapabilityCatalogV1Version || catalog.PolicyVersion != RoutingPolicyV1Version {
		return contractError(CodeUnsupportedVersion, "unsupported capability catalog or policy version", "")
	}
	if len(catalog.Models) == 0 || len(catalog.Models) > MaxCatalogModels {
		return contractError(CodeInvalidContract, "catalog model count is outside its bound", "")
	}
	seen := map[string]bool{}
	lastID := ""
	for _, model := range catalog.Models {
		if err := ValidateModelCapability(model); err != nil {
			return err
		}
		if seen[model.ID] {
			return contractError(CodeDuplicateIdentity, "catalog model ID is duplicated", model.ID)
		}
		if lastID != "" && model.ID <= lastID {
			return contractError(CodeInvalidContract, "catalog models are not in canonical ID order", model.ID)
		}
		seen[model.ID] = true
		lastID = model.ID
	}
	return nil
}

func ValidateModelCapability(model ModelCapabilityV1) error {
	if !validIdentity(model.ID) || !validTarget(model.Target) {
		return contractError(CodeInvalidTarget, "model identity or target is invalid", model.ID)
	}
	if model.ContextLimitBytes < 1 || model.ContextLimitBytes > MaxRoutingBudgetBytes || model.MaxOutputBytes < 1 || model.MaxOutputBytes > model.ContextLimitBytes {
		return contractError(CodeInvalidBudget, "model context/output limits are invalid", model.ID)
	}
	if !validQuality(model.Quality) || !validCost(model.Cost) || !validLatency(model.Latency) {
		return contractError(CodeInvalidCapability, "model quality, cost, or latency class is invalid", model.ID)
	}
	if len(model.Capabilities) == 0 {
		return contractError(CodeInvalidCapability, "model must declare at least one capability", model.ID)
	}
	if err := validateCapabilities(model.Capabilities); err != nil {
		return err
	}
	return nil
}

func ValidateTarget(target Target) error {
	if !validTarget(target) {
		return contractError(CodeInvalidTarget, "routing target is invalid", target.Model)
	}
	return nil
}

func ValidateRequirement(requirement RoutingRequirementV1) error {
	if requirement.SchemaVersion != RoutingRequirementV1Version {
		return contractError(CodeUnsupportedVersion, "unsupported routing requirement version", "")
	}
	if len(requirement.Capabilities) == 0 {
		return contractError(CodeInvalidCapability, "routing requirement must declare capabilities", "")
	}
	if err := validateCapabilities(requirement.Capabilities); err != nil {
		return err
	}
	if !validComplexity(requirement.Complexity) || !validQuality(requirement.Quality) || !validLatency(requirement.Latency) {
		return contractError(CodeInvalidContract, "routing requirement class is invalid", "")
	}
	if requirement.MaxCostUnits < 1 || requirement.MaxCostUnits > MaxCostUnits || requirement.MaxContextBytes < 1 || requirement.MaxContextBytes > MaxRoutingBudgetBytes || requirement.MaxOutputBytes < 1 || requirement.MaxOutputBytes > requirement.MaxContextBytes {
		return contractError(CodeInvalidBudget, "routing requirement budget must be positive", "")
	}
	if len(requirement.Candidates) > MaxCandidates {
		return contractError(CodeInvalidTarget, "routing candidate count exceeds its bound", "")
	}
	seen := map[string]bool{}
	for _, candidate := range requirement.Candidates {
		if !validIdentity(candidate) {
			return contractError(CodeInvalidTarget, "routing candidate identity is invalid", candidate)
		}
		if seen[candidate] {
			return contractError(CodeDuplicateIdentity, "routing candidate is duplicated", candidate)
		}
		seen[candidate] = true
	}
	if requirement.PromptProfile != "" && !validIdentity(requirement.PromptProfile) {
		return contractError(CodeInvalidPromptProfile, "prompt profile identity is invalid", requirement.PromptProfile)
	}
	return nil
}

func ValidateBudgetGrant(b BudgetGrantV1) error {
	if b.MaxCostUnits < 1 || b.MaxCostUnits > MaxCostUnits || b.ContextBytes < 1 || b.ContextBytes > MaxRoutingBudgetBytes || b.OutputBytes < 1 || b.OutputBytes > b.ContextBytes {
		return contractError(CodeInvalidBudget, "budget grant values must be positive", "")
	}
	return nil
}

func ValidateFallbackPolicy(p FallbackPolicyV1) error {
	if p.Mode != FallbackNone && p.Mode != FallbackEligible {
		return contractError(CodeInvalidFallback, "unsupported fallback mode", "")
	}
	if p.MaxAttempts < 1 || p.MaxAttempts > MaxFallbacks+1 {
		return contractError(CodeInvalidFallback, "fallback attempt count is outside its bound", "")
	}
	if p.Mode == FallbackNone && p.MaxAttempts != 1 {
		return contractError(CodeInvalidFallback, "fallback-none policy must allow exactly one attempt", "")
	}
	if p.Mode == FallbackEligible && !p.RequireNoSideEffect {
		return contractError(CodeInvalidFallback, "eligible fallback must require no side effects", "")
	}
	return nil
}

func ValidatePromptProfile(p PromptProfileV1) error {
	if p.SchemaVersion != PromptProfileV1Version || !validIdentity(p.ID) || strings.TrimSpace(p.Version) == "" || p.Version != strings.TrimSpace(p.Version) || len([]byte(p.Version)) > 64 || len(p.Components) == 0 || len(p.Components) > MaxPromptComponents {
		return contractError(CodeInvalidPromptProfile, "prompt profile identity or bounds are invalid", p.ID)
	}
	seen := map[string]bool{}
	for _, component := range p.Components {
		if !validIdentity(component.ID) || seen[component.ID] {
			return contractError(CodeInvalidPromptProfile, "prompt profile component is invalid or duplicated", component.ID)
		}
		seen[component.ID] = true
	}
	return nil
}

func ValidateDecision(d RoutingDecisionV1) error {
	if d.SchemaVersion != RoutingDecisionV1Version || !validHash(d.CatalogHash) || !validTarget(d.Selected) {
		return contractError(CodeInvalidContract, "routing decision identity is invalid", "")
	}
	if err := ValidateRequirement(d.Requirement); err != nil {
		return err
	}
	if err := ValidateBudgetGrant(d.Budget); err != nil {
		return err
	}
	if err := ValidateFallbackPolicy(d.FallbackPolicy); err != nil {
		return err
	}
	if len(d.ReasonCodes) == 0 || len(d.ReasonCodes) > MaxReasonCodes {
		return contractError(CodeInvalidContract, "routing decision must contain bounded reason codes", "")
	}
	seen := map[string]bool{}
	for _, reason := range d.ReasonCodes {
		if !reasonPattern.MatchString(reason) || seen[reason] {
			return contractError(CodeInvalidContract, "routing reason code is invalid or duplicated", reason)
		}
		seen[reason] = true
	}
	if len(d.Fallback) > MaxFallbacks || d.FallbackPolicy.Mode == FallbackNone && len(d.Fallback) != 0 {
		return contractError(CodeInvalidFallback, "fallback targets do not match policy", "")
	}
	for _, target := range d.Fallback {
		if !validTarget(target) || target == d.Selected {
			return contractError(CodeInvalidFallback, "fallback target is invalid or duplicates the selected target", "")
		}
	}
	if d.PromptProfile != "" && !validIdentity(d.PromptProfile) {
		return contractError(CodeInvalidPromptProfile, "decision prompt profile identity is invalid", d.PromptProfile)
	}
	return nil
}

func ValidateRoutingUsage(usage RoutingUsageV1) error {
	if usage.SchemaVersion != RoutingUsageV1Version || !validTarget(usage.Target) {
		return contractError(CodeInvalidContract, "routing usage identity is invalid", "")
	}
	if usage.RouteIndex < 0 || usage.RouteIndex > MaxFallbacks {
		return contractError(CodeInvalidFallback, "routing usage route index is outside its bound", "")
	}
	if usage.CostUnits < 1 || usage.CostUnits > MaxCostUnits || usage.CumulativeCostUnits < usage.CostUnits || usage.CumulativeCostUnits > MaxCostUnits {
		return contractError(CodeInvalidBudget, "routing usage cost is outside its bound", "")
	}
	return nil
}

func CatalogHash(catalog CapabilityCatalogV1) (string, error) {
	if err := ValidateCatalog(catalog); err != nil {
		return "", err
	}
	encoded, err := canonicalJSON(catalog)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func CanonicalJSON(value any) ([]byte, error) { return canonicalJSON(value) }

func canonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, encoded); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}

func validIdentity(value string) bool { return identityPattern.MatchString(value) }

func validTarget(t Target) bool {
	return validIdentity(t.Driver) && validIdentity(t.Provider) && validIdentity(t.Model)
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateCapabilities(values []Capability) error {
	seen := map[Capability]bool{}
	last := Capability("")
	for _, value := range values {
		if !containsCapability(value) || seen[value] {
			return contractError(CodeInvalidCapability, "capability is unsupported or duplicated", string(value))
		}
		if last != "" && value <= last {
			return contractError(CodeInvalidCapability, "capabilities are not in canonical order", string(value))
		}
		seen[value] = true
		last = value
	}
	return nil
}

func containsCapability(value Capability) bool {
	for _, known := range StableCapabilities {
		if value == known {
			return true
		}
	}
	return false
}

func validComplexity(v Complexity) bool {
	return v == ComplexitySimple || v == ComplexityStandard || v == ComplexityComplex
}
func validQuality(v QualityClass) bool {
	return v == QualityEconomy || v == QualityBalanced || v == QualityPremium
}
func validCost(v CostClass) bool { return v == CostLow || v == CostMedium || v == CostHigh }
func validLatency(v LatencyClass) bool {
	return v == LatencyFast || v == LatencyBalanced || v == LatencySlow
}

func SortCapabilities(values []Capability) []Capability {
	result := append([]Capability(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
