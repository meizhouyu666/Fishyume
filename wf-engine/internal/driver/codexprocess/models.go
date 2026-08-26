package codexprocess

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const (
	maxDiscoveredModels = 256
	maxModelPages       = 16
	modelPageSize       = 100
	defaultProbeTimeout = 45 * time.Second
)

type ModelInfo struct {
	ID                     string   `json:"id"`
	Model                  string   `json:"model"`
	DisplayName            string   `json:"displayName"`
	Description            string   `json:"description,omitempty"`
	Hidden                 bool     `json:"hidden"`
	Default                bool     `json:"default"`
	SupportedEfforts       []string `json:"supportedReasoningEfforts"`
	DefaultEffort          string   `json:"defaultReasoningEffort"`
	InputModalities        []string `json:"inputModalities,omitempty"`
	ServiceTiers           []string `json:"serviceTiers,omitempty"`
	DefaultServiceTier     string   `json:"defaultServiceTier,omitempty"`
	SupportsPersonality    bool     `json:"supportsPersonality"`
	SupportsMultiAgentMode bool     `json:"supportsMultiAgentMode"`
}

type ProbeResult struct {
	Model      string        `json:"model"`
	Effort     string        `json:"reasoningEffort"`
	Available  bool          `json:"available"`
	Diagnostic string        `json:"diagnostic"`
	Duration   time.Duration `json:"-"`
}

type modelListResponse struct {
	Data []struct {
		ID                        string   `json:"id"`
		Model                     string   `json:"model"`
		DisplayName               string   `json:"displayName"`
		Description               string   `json:"description"`
		Hidden                    bool     `json:"hidden"`
		IsDefault                 bool     `json:"isDefault"`
		DefaultReasoningEffort    string   `json:"defaultReasoningEffort"`
		InputModalities           []string `json:"inputModalities"`
		SupportsPersonality       bool     `json:"supportsPersonality"`
		MultiAgentVersion         any      `json:"multiAgentVersion"`
		DefaultServiceTier        *string  `json:"defaultServiceTier"`
		SupportedReasoningEfforts []struct {
			ReasoningEffort string `json:"reasoningEffort"`
		} `json:"supportedReasoningEfforts"`
		ServiceTiers []struct {
			ID string `json:"id"`
		} `json:"serviceTiers"`
	} `json:"data"`
	NextCursor *string `json:"nextCursor"`
}

func (b *Backend) DiscoverModels(ctx context.Context) ([]ModelInfo, error) {
	if b == nil {
		return nil, fmt.Errorf("Codex Backend is unavailable")
	}
	discovered, err := b.discoverExecutable()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(b.config.StateRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create Codex state root: %w", err)
	}
	client, err := startAppServer(ctx, discovered.Path, b.config.StateRoot, b.config.MaxStderrBytes)
	if err != nil {
		return nil, err
	}
	defer client.close()

	models := make([]ModelInfo, 0, 16)
	seenModels := make(map[string]bool)
	cursor := ""
	for page := 0; page < maxModelPages; page++ {
		params := map[string]any{"limit": modelPageSize, "includeHidden": true}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var response modelListResponse
		if err := client.request(ctx, "model/list", params, &response); err != nil {
			return nil, err
		}
		for _, candidate := range response.Data {
			if candidate.Model == "" || candidate.Model != strings.TrimSpace(candidate.Model) || len(candidate.Model) > 128 || seenModels[candidate.Model] {
				continue
			}
			seenModels[candidate.Model] = true
			efforts := make([]string, 0, len(candidate.SupportedReasoningEfforts))
			seenEfforts := make(map[string]bool)
			for _, option := range candidate.SupportedReasoningEfforts {
				effort := strings.TrimSpace(option.ReasoningEffort)
				if effort != "" && len(effort) <= 32 && !seenEfforts[effort] {
					seenEfforts[effort] = true
					efforts = append(efforts, effort)
				}
			}
			serviceTiers := make([]string, 0, len(candidate.ServiceTiers))
			for _, tier := range candidate.ServiceTiers {
				if value := strings.TrimSpace(tier.ID); value != "" && len(value) <= 64 {
					serviceTiers = append(serviceTiers, value)
				}
			}
			defaultTier := ""
			if candidate.DefaultServiceTier != nil {
				defaultTier = strings.TrimSpace(*candidate.DefaultServiceTier)
			}
			models = append(models, ModelInfo{
				ID: candidate.ID, Model: candidate.Model, DisplayName: candidate.DisplayName,
				Description: candidate.Description, Hidden: candidate.Hidden, Default: candidate.IsDefault,
				SupportedEfforts: efforts, DefaultEffort: candidate.DefaultReasoningEffort,
				InputModalities: append([]string(nil), candidate.InputModalities...), ServiceTiers: serviceTiers,
				DefaultServiceTier: defaultTier, SupportsPersonality: candidate.SupportsPersonality,
				SupportsMultiAgentMode: candidate.MultiAgentVersion != nil,
			})
			if len(models) > maxDiscoveredModels {
				return nil, fmt.Errorf("Codex model/list exceeded %d models", maxDiscoveredModels)
			}
		}
		if response.NextCursor == nil || strings.TrimSpace(*response.NextCursor) == "" {
			sort.Slice(models, func(i, j int) bool { return models[i].Model < models[j].Model })
			return models, nil
		}
		next := strings.TrimSpace(*response.NextCursor)
		if next == cursor || len(next) > 1024 {
			return nil, fmt.Errorf("Codex model/list returned an invalid pagination cursor")
		}
		cursor = next
	}
	return nil, fmt.Errorf("Codex model/list exceeded %d pages", maxModelPages)
}

func (b *Backend) ProbeModel(ctx context.Context, model, effort string) ProbeResult {
	started := time.Now()
	result := ProbeResult{Model: strings.TrimSpace(model), Effort: strings.TrimSpace(effort)}
	if result.Model == "" || len(result.Model) > 128 || result.Effort == "" || len(result.Effort) > 32 {
		result.Diagnostic = "model and reasoning effort are required"
		return result
	}
	discovered, err := b.discoverExecutable()
	if err != nil {
		result.Diagnostic = boundedProbeDiagnostic(err.Error())
		return result
	}
	if err := os.MkdirAll(b.config.StateRoot, 0o700); err != nil {
		result.Diagnostic = boundedProbeDiagnostic(err.Error())
		return result
	}
	probeCtx, cancel := context.WithTimeout(ctx, defaultProbeTimeout)
	defer cancel()
	command := exec.CommandContext(probeCtx, discovered.Path,
		"exec", "--ephemeral", "--model", result.Model,
		"-c", "model_reasoning_effort=\""+result.Effort+"\"",
		"--sandbox", "read-only", "--json", "--color", "never",
		"--skip-git-repo-check", "-C", b.config.StateRoot, "-",
	)
	configureBackgroundCommand(command)
	command.Stdin = strings.NewReader("Reply with exactly OK. Do not use tools.\n")
	stdout := &boundedWriter{limit: 64 * 1024}
	stderr := &boundedWriter{limit: 64 * 1024}
	command.Stdout, command.Stderr = stdout, stderr
	err = command.Run()
	result.Duration = time.Since(started)
	if err != nil {
		diagnostic := strings.TrimSpace(stderr.String())
		if diagnostic == "" {
			diagnostic = err.Error()
		}
		if probeCtx.Err() != nil {
			diagnostic = "probe timed out: " + probeCtx.Err().Error()
		}
		result.Diagnostic = boundedProbeDiagnostic(diagnostic)
		return result
	}
	result.Available = true
	result.Diagnostic = "Codex completed a read-only probe"
	return result
}

func boundedProbeDiagnostic(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 1024 {
		value = value[:1024]
	}
	return value
}
