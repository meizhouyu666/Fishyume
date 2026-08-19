package application

import (
	"encoding/json"
	"time"

	"wf.local/wf-engine/internal/contextcompiler"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

const (
	APIVersion             = "fishyume.application/v1"
	WorkflowSchemaVersion  = "fishyume/v2"
	DefaultListLimit       = 50
	MaxListLimit           = 100
	DefaultEventLimit      = 50
	MaxEventLimit          = 100
	MaxEventWaitMS         = 30_000
	MaxResponseBytes       = 512 * 1024
	MaxSchemaResponseBytes = 256 * 1024
	MaxErrorDataBytes      = 8 * 1024
	MaxRequestIDBytes      = 256
	AuthoringGuideVersion  = "fishyume.authoring-guide/v1"
	MaxAuthoringFlowSteps  = 16
	MaxAuthoringRules      = 16
	MaxAuthoringRuleBytes  = 1024
)

type JSONScalar any

type WorkflowSource struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

type WorkflowInput struct {
	Source   *WorkflowSource `json:"source,omitempty"`
	Document json.RawMessage `json:"document,omitempty"`
}

type DriverCapability struct {
	Driver                   string   `json:"driver"`
	Targets                  []string `json:"targets"`
	Ready                    bool     `json:"ready"`
	Diagnostic               string   `json:"diagnostic,omitempty"`
	MaxConcurrentAgents      int      `json:"maxConcurrentAgents"`
	SupportsConcurrentCancel bool     `json:"supportsConcurrentCancel"`
}

type Limits struct {
	DefaultListLimit        int `json:"defaultListLimit"`
	MaxListLimit            int `json:"maxListLimit"`
	DefaultEventLimit       int `json:"defaultEventLimit"`
	MaxEventLimit           int `json:"maxEventLimit"`
	MaxEventWaitMS          int `json:"maxEventWaitMs"`
	MaxResponseBytes        int `json:"maxResponseBytes"`
	MaxSchemaResponseBytes  int `json:"maxSchemaResponseBytes"`
	MaxErrorDataBytes       int `json:"maxErrorDataBytes"`
	MaxRequestIDBytes       int `json:"maxRequestIdBytes"`
	MaxMemoryContentBytes   int `json:"maxMemoryContentBytes"`
	MaxProjectMemoryRecords int `json:"maxProjectMemoryRecords"`
	MaxMemorySupersedes     int `json:"maxMemorySupersedes"`
	MaxMemoryReceipts       int `json:"maxMemoryReceipts"`
	DefaultMemoryListLimit  int `json:"defaultMemoryListLimit"`
	MaxMemoryListLimit      int `json:"maxMemoryListLimit"`
}

type SystemCapabilitiesRequest struct {
	Project string `json:"project,omitempty"`
}

// AuthoringGuide is the bounded, Provider-independent discovery contract for Host
// Agents. It deliberately contains instructions about public API sequencing only;
// dynamic project content, prompts, credentials, and Memory content never belong here.
type AuthoringGuide struct {
	SchemaVersion      string   `json:"schemaVersion"`
	RecommendedFlow    []string `json:"recommendedFlow"`
	WorkflowAPIVersion string   `json:"workflowApiVersion"`
	Rules              []string `json:"rules"`
}

type SystemCapabilitiesResponse struct {
	APIVersion            string             `json:"apiVersion"`
	WorkflowSchemaVersion string             `json:"workflowSchemaVersion"`
	WorkflowSchema        json.RawMessage    `json:"workflowSchema"`
	NodeTypes             []string           `json:"nodeTypes"`
	ActionTypes           []ActionType       `json:"actionTypes"`
	Drivers               []DriverCapability `json:"drivers"`
	Limits                Limits             `json:"limits"`
	ErrorCodes            []ErrorCode        `json:"errorCodes"`
	MinimalExample        json.RawMessage    `json:"minimalExample"`
	AuthoringGuide        AuthoringGuide     `json:"authoringGuide"`
}

type ValidationIssue struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type WorkflowValidateRequest struct {
	Project         string                   `json:"project,omitempty"`
	Workflow        WorkflowInput            `json:"workflow"`
	Inputs          map[string]any           `json:"inputs,omitempty"`
	Driver          string                   `json:"driver,omitempty"`
	Target          string                   `json:"target,omitempty"`
	ContextBindings workflow.ContextBindings `json:"contextBindings,omitempty"`
}

type WorkflowValidateResponse struct {
	APIVersion            string            `json:"apiVersion"`
	WorkflowSchemaVersion string            `json:"workflowSchemaVersion"`
	Valid                 bool              `json:"valid"`
	Issues                []ValidationIssue `json:"issues"`
	CapabilityGaps        []ValidationIssue `json:"capabilityGaps"`
	Warnings              []string          `json:"warnings"`
}

type ResolvedAgent struct {
	Driver string `json:"driver"`
	Target string `json:"target"`
}

type ExplainNode struct {
	ID                   string                   `json:"id"`
	Type                 string                   `json:"type"`
	DependsOn            []string                 `json:"dependsOn"`
	ParallelLayer        int                      `json:"parallelLayer"`
	ApprovalPrompt       string                   `json:"approvalPrompt,omitempty"`
	Condition            json.RawMessage          `json:"condition,omitempty"`
	ContextSources       []string                 `json:"contextSources"`
	ProjectInstructions  []string                 `json:"projectInstructions,omitempty"`
	MemoryBindings       []workflow.MemoryBinding `json:"memoryBindings,omitempty"`
	ContextPolicyVersion string                   `json:"contextPolicyVersion"`
	Agent                *ResolvedAgent           `json:"agent,omitempty"`
}

type WorkflowExplainRequest = WorkflowValidateRequest

type WorkflowExplainResponse struct {
	APIVersion            string            `json:"apiVersion"`
	WorkflowSchemaVersion string            `json:"workflowSchemaVersion"`
	Name                  string            `json:"name"`
	TopologicalOrder      []string          `json:"topologicalOrder"`
	ParallelLayers        [][]string        `json:"parallelLayers"`
	Nodes                 []ExplainNode     `json:"nodes"`
	CapabilityGaps        []ValidationIssue `json:"capabilityGaps"`
	Warnings              []string          `json:"warnings"`
}

type RunStartRequest struct {
	Project         string                   `json:"project"`
	Workflow        WorkflowInput            `json:"workflow"`
	Inputs          map[string]any           `json:"inputs,omitempty"`
	Driver          string                   `json:"driver,omitempty"`
	Target          string                   `json:"target,omitempty"`
	ClientRequestID string                   `json:"clientRequestId"`
	ContextBindings workflow.ContextBindings `json:"contextBindings,omitempty"`
}

type RunStartResponse struct {
	APIVersion   string `json:"apiVersion"`
	RunID        string `json:"runId"`
	StateVersion uint64 `json:"stateVersion"`
	Attach       string `json:"attach"`
}

type RunFilter struct {
	Project    string `json:"project,omitempty"`
	Phase      string `json:"phase,omitempty"`
	Conclusion string `json:"conclusion,omitempty"`
}

type RunListRequest struct {
	Filter RunFilter `json:"filter,omitempty"`
	Cursor string    `json:"cursor,omitempty"`
	Limit  int       `json:"limit,omitempty"`
}

type RunSummary struct {
	RunID        string `json:"runId"`
	WorkflowName string `json:"workflowName"`
	Project      string `json:"project"`
	Driver       string `json:"driver"`
	Target       string `json:"target"`
	Phase        string `json:"phase"`
	Conclusion   string `json:"conclusion,omitempty"`
	StateVersion uint64 `json:"stateVersion"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

type RunListResponse struct {
	APIVersion string       `json:"apiVersion"`
	Items      []RunSummary `json:"items"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

type RunGetRequest struct {
	RunID string `json:"runId"`
}

type Question struct {
	ID       string   `json:"id"`
	Prompt   string   `json:"prompt"`
	Choices  []string `json:"choices"`
	Required bool     `json:"required"`
}

type Result struct {
	Summary   string         `json:"summary,omitempty"`
	Artifacts []string       `json:"artifacts"`
	Warnings  []string       `json:"warnings"`
	Checks    []string       `json:"checks"`
	Questions []Question     `json:"questions"`
	Decision  string         `json:"decision,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Usage     map[string]int `json:"usage,omitempty"`
}

type AttemptView struct {
	Number      int                 `json:"number"`
	Phase       string              `json:"phase"`
	Conclusion  string              `json:"conclusion,omitempty"`
	Reason      string              `json:"reason,omitempty"`
	Driver      string              `json:"driver"`
	Target      string              `json:"target"`
	ContextHash string              `json:"contextHash,omitempty"`
	Context     *ContextInspect     `json:"context,omitempty"`
	MemoryUsage *MemoryUsageInspect `json:"memoryUsage,omitempty"`
	StartedAt   string              `json:"startedAt"`
	UpdatedAt   string              `json:"updatedAt"`
	CompletedAt string              `json:"completedAt,omitempty"`
}

type ContextComponentInspect struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	Tier             string `json:"tier"`
	SelectionReason  string `json:"selectionReason,omitempty"`
	ProvenanceSource string `json:"source,omitempty"`
	OriginalBytes    int    `json:"originalBytes,omitempty"`
	IncludedBytes    int    `json:"includedBytes,omitempty"`
	Truncation       string `json:"truncation"`
}
type ContextOmissionInspect struct {
	ID            string `json:"id"`
	Kind          string `json:"kind,omitempty"`
	Reason        string `json:"reason,omitempty"`
	OriginalBytes int    `json:"originalBytes,omitempty"`
}
type MemoryUsageInspect struct {
	RecordIDs []string `json:"recordIds"`
	Committed bool     `json:"committed"`
}
type ContextInspect struct {
	SchemaVersion   string                    `json:"schemaVersion"`
	CompilerVersion string                    `json:"compilerVersion"`
	Hash            string                    `json:"hash,omitempty"`
	Budget          map[string]int            `json:"budget"`
	Usage           map[string]int            `json:"usage"`
	Components      []ContextComponentInspect `json:"components"`
	Omissions       []ContextOmissionInspect  `json:"omissions,omitempty"`
	Truncated       bool                      `json:"truncated"`
	MemoryUsage     *MemoryUsageInspect       `json:"memoryUsage,omitempty"`
}

type NodeView struct {
	NodeID         string       `json:"nodeId"`
	Type           string       `json:"type"`
	Phase          string       `json:"phase"`
	Conclusion     string       `json:"conclusion,omitempty"`
	Reason         string       `json:"reason,omitempty"`
	Diagnostic     string       `json:"diagnostic,omitempty"`
	CurrentAttempt int          `json:"currentAttempt,omitempty"`
	Attempt        *AttemptView `json:"attempt,omitempty"`
	Result         *Result      `json:"result,omitempty"`
}

type RunView struct {
	RunSummary
	Summary              string     `json:"summary,omitempty"`
	CancelRequested      bool       `json:"cancelRequested"`
	EffectiveConcurrency int        `json:"effectiveConcurrency"`
	TopologicalOrder     []string   `json:"topologicalOrder"`
	Nodes                []NodeView `json:"nodes"`
	DeprecationWarnings  []string   `json:"deprecationWarnings"`
}

type RunGetResponse struct {
	APIVersion string  `json:"apiVersion"`
	Run        RunView `json:"run"`
}

type RunEventsRequest struct {
	RunID         string `json:"runId"`
	AfterSequence uint64 `json:"afterSequence,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	WaitMS        int    `json:"waitMs,omitempty"`
}

type Event struct {
	RunID      string `json:"runId"`
	Sequence   uint64 `json:"sequence"`
	Type       string `json:"type"`
	Phase      string `json:"phase"`
	Conclusion string `json:"conclusion,omitempty"`
	Reason     string `json:"reason,omitempty"`
	NodeID     string `json:"nodeId,omitempty"`
	NodePhase  string `json:"nodePhase,omitempty"`
	Message    string `json:"message,omitempty"`
	Timestamp  string `json:"timestamp"`
}

type RunEventsResponse struct {
	APIVersion        string  `json:"apiVersion"`
	RunID             string  `json:"runId"`
	Events            []Event `json:"events"`
	NextAfterSequence uint64  `json:"nextAfterSequence"`
	More              bool    `json:"more"`
}

type ActionType string

const (
	ActionApprove ActionType = "approve"
	ActionReject  ActionType = "reject"
	ActionAnswer  ActionType = "answer"
	ActionRetry   ActionType = "retry"
	ActionCancel  ActionType = "cancel"
)

type RunActionRequest struct {
	ActionID                 string         `json:"actionId"`
	RunID                    string         `json:"runId"`
	Type                     ActionType     `json:"type"`
	ExpectedStateVersion     uint64         `json:"expectedStateVersion"`
	NodeID                   string         `json:"nodeId,omitempty"`
	ExpectedAttempt          *int           `json:"expectedAttempt,omitempty"`
	Reason                   string         `json:"reason,omitempty"`
	Answers                  map[string]any `json:"answers,omitempty"`
	AcknowledgeDuplicateRisk bool           `json:"acknowledgeDuplicateRisk,omitempty"`
}

type RunActionResponse struct {
	APIVersion   string     `json:"apiVersion"`
	ActionID     string     `json:"actionId"`
	RunID        string     `json:"runId"`
	Type         ActionType `json:"type"`
	StateVersion uint64     `json:"stateVersion"`
	Phase        string     `json:"phase"`
	Conclusion   string     `json:"conclusion,omitempty"`
}

type RunResultRequest struct {
	RunID string `json:"runId"`
}

type NodeResult struct {
	NodeID     string  `json:"nodeId"`
	Conclusion string  `json:"conclusion,omitempty"`
	Result     *Result `json:"result,omitempty"`
}

type RunResultResponse struct {
	APIVersion  string       `json:"apiVersion"`
	RunID       string       `json:"runId"`
	Conclusion  string       `json:"conclusion"`
	Summary     string       `json:"summary,omitempty"`
	Results     []NodeResult `json:"results"`
	CompletedAt string       `json:"completedAt"`
}

type MemoryCreateRequest struct {
	Project     string                      `json:"project"`
	MutationID  string                      `json:"mutationId"`
	Type        contextcompiler.MemoryType  `json:"type"`
	Content     string                      `json:"content"`
	Sensitivity contextcompiler.Sensitivity `json:"sensitivity"`
	Reason      string                      `json:"reason"`
	ExpiresAt   string                      `json:"expiresAt,omitempty"`
	MaxUses     int                         `json:"maxUses,omitempty"`
}

type MemoryGetRequest struct {
	Project  string `json:"project"`
	RecordID string `json:"recordId"`
}

type MemoryListFilter struct {
	Type        contextcompiler.MemoryType   `json:"type,omitempty"`
	State       contextcompiler.MemoryState  `json:"state,omitempty"`
	Sensitivity contextcompiler.Sensitivity  `json:"sensitivity,omitempty"`
	Writer      contextcompiler.MemoryWriter `json:"writer,omitempty"`
}

type MemoryListRequest struct {
	Project string           `json:"project"`
	Filter  MemoryListFilter `json:"filter,omitempty"`
	Cursor  string           `json:"cursor,omitempty"`
	Limit   int              `json:"limit,omitempty"`
}

type MemorySupersedeRequest struct {
	Project     string                      `json:"project"`
	MutationID  string                      `json:"mutationId"`
	Supersedes  []string                    `json:"supersedes"`
	Type        contextcompiler.MemoryType  `json:"type"`
	Content     string                      `json:"content"`
	Sensitivity contextcompiler.Sensitivity `json:"sensitivity"`
	Reason      string                      `json:"reason"`
	ExpiresAt   string                      `json:"expiresAt,omitempty"`
	MaxUses     int                         `json:"maxUses,omitempty"`
}

type MemoryDeleteRequest struct {
	Project    string `json:"project"`
	MutationID string `json:"mutationId"`
	RecordID   string `json:"recordId"`
	Reason     string `json:"reason"`
}

type MemoryMutationResponse struct {
	APIVersion  string   `json:"apiVersion"`
	Revision    uint64   `json:"revision"`
	RecordID    string   `json:"recordId"`
	AffectedIDs []string `json:"affectedIds"`
	Replayed    bool     `json:"replayed"`
}

type MemoryGetResponse struct {
	APIVersion string                         `json:"apiVersion"`
	Revision   uint64                         `json:"revision"`
	Record     contextcompiler.MemoryRecordV1 `json:"record"`
}

type MemoryRecordMetadata = store.MemoryRecordMetadata

type MemoryListResponse struct {
	APIVersion string                 `json:"apiVersion"`
	Revision   uint64                 `json:"revision"`
	Items      []MemoryRecordMetadata `json:"items"`
	NextCursor string                 `json:"nextCursor,omitempty"`
}

func StableLimits() Limits {
	return Limits{DefaultListLimit: DefaultListLimit, MaxListLimit: MaxListLimit, DefaultEventLimit: DefaultEventLimit, MaxEventLimit: MaxEventLimit, MaxEventWaitMS: MaxEventWaitMS, MaxResponseBytes: MaxResponseBytes, MaxSchemaResponseBytes: MaxSchemaResponseBytes, MaxErrorDataBytes: MaxErrorDataBytes, MaxRequestIDBytes: MaxRequestIDBytes, MaxMemoryContentBytes: contextcompiler.MaxMemoryContentBytes, MaxProjectMemoryRecords: contextcompiler.MaxProjectMemoryRecords, MaxMemorySupersedes: contextcompiler.MaxMemorySupersedes, MaxMemoryReceipts: store.MaxMemoryReceipts, DefaultMemoryListLimit: store.DefaultMemoryListLimit, MaxMemoryListLimit: store.MaxMemoryListLimit}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
