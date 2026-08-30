package routingconfig

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"wf.local/wf-engine/internal/driver/codexprocess"
)

const inventoryVersion = "fishyume.driver-inventory/v1"

// inventoryProbeTimeout bounds each installed-CLI subprocess so a hung or
// interactive command cannot stall Engine startup or the settings page.
const inventoryProbeTimeout = 5 * time.Second

// DriverInventoryEntry is the unified, per-harness environment fact set:
// whether the CLI is installed, its resolved version, whether it reports an
// authenticated identity, and the models it exposes. It is intentionally
// surface-neutral: "supported" is a product capability declared separately by
// system.capabilities (workflow) and team.capabilities (team).
type DriverInventoryEntry struct {
	Driver        string    `json:"driver"`
	Installed     bool      `json:"installed"`
	Version       string    `json:"version,omitempty"`
	Authenticated bool      `json:"authenticated"`
	Models        []string  `json:"models"`
	Executable    string    `json:"executable,omitempty"`
	Diagnostic    string    `json:"diagnostic,omitempty"`
	ObservedAt    time.Time `json:"observedAt"`
}

type DriverInventoryResponse struct {
	SchemaVersion string                  `json:"schemaVersion"`
	Drivers       []DriverInventoryEntry `json:"drivers"`
}

// DriverInventory probes the three Team harnesses (codex, claude, opencode)
// for their installed/version/authenticated/model facts. It reuses the cached
// executable discovery and Codex model discovery; version and auth are probed
// with a bounded subprocess timeout and never block on interactive prompts.
func (s *Service) DriverInventory() DriverInventoryResponse {
	s.mu.Lock()
	statuses := cloneTeamDrivers(s.teamDrivers.Drivers)
	codexModels := modelNames(s.discovery.Models)
	s.mu.Unlock()

	status := map[string]TeamDriverStatus{}
	for _, item := range statuses {
		status[item.Driver] = item
	}
	now := s.now().UTC()
	drivers := make([]DriverInventoryEntry, 0, 3)
	for _, name := range []string{"codex", "claude", "opencode"} {
		found := status[name]
		entry := DriverInventoryEntry{Driver: name, Installed: found.Available, Executable: found.Executable, Diagnostic: found.Diagnostic, Models: []string{}, ObservedAt: now}
		if name == "codex" {
			entry.Models = codexModels
		}
		if found.Available && found.Executable != "" {
			entry.Version = probeVersion(found.Executable)
			entry.Authenticated = probeAuthenticated(name, found.Executable)
			if entry.Version == "" && entry.Diagnostic == "" {
				entry.Diagnostic = name + " CLI did not report a version"
			}
		}
		drivers = append(drivers, entry)
	}
	return DriverInventoryResponse{SchemaVersion: inventoryVersion, Drivers: drivers}
}

func modelNames(models []codexprocess.ModelInfo) []string {
	result := make([]string, 0, len(models))
	for _, model := range models {
		if name := strings.TrimSpace(model.Model); name != "" {
			result = append(result, name)
		}
	}
	return result
}

// probeVersion runs `<exe> --version` and returns the first non-empty line,
// bounded. It reports "" when the command fails or times out.
func probeVersion(executable string) string {
	output, _, _ := probeCLI(executable, "--version")
	for _, line := range strings.Split(output, "\n") {
		if value := strings.TrimSpace(line); value != "" {
			return boundedDiagnostic(value)
		}
	}
	return ""
}

// probeAuthenticated runs each harness's non-interactive identity command and
// reports true only when the command exits 0 and prints a "logged in" signal.
func probeAuthenticated(name, executable string) bool {
	args := authProbeArgs(name)
	if len(args) == 0 {
		return false
	}
	output, code, err := probeCLI(executable, args...)
	if err != nil || code != 0 {
		return false
	}
	lower := strings.ToLower(output)
	return strings.Contains(lower, "logged in") || strings.Contains(lower, "loggedin")
}

func authProbeArgs(name string) []string {
	switch name {
	case "codex":
		return []string{"login", "status"}
	case "claude":
		return []string{"auth", "status"}
	case "opencode":
		return []string{"auth", "list"}
	default:
		return nil
	}
}

// probeCLI runs a bounded subprocess and returns its combined output and exit
// code. A timeout or failure to launch is reported through the error value.
func probeCLI(executable string, args ...string) (output string, exitCode int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), inventoryProbeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	var builder strings.Builder
	command.Stdout = &builder
	command.Stderr = &builder
	runErr := command.Run()
	code := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else if ctx.Err() != nil {
			err = ctx.Err()
			return "", 0, err
		} else {
			err = runErr
			return "", 0, err
		}
	}
	return builder.String(), code, nil
}
