package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"wf.local/wf-engine/internal/driver/codexprocess"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/team"
	"wf.local/wf-engine/internal/teamcontract"
)

func TestOneRoundPanelUsesDistinctModelsWithoutRunsMemoryOrWorkspaceWrites(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("process recovery is supported on Windows and Linux")
	}
	moduleRoot, fixtureDir := moduleDirectory(t), t.TempDir()
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	agentPath := filepath.Join(fixtureDir, "fake-codex"+extension)
	supervisorPath := filepath.Join(fixtureDir, "fishyume-engine"+extension)
	buildFixture(t, moduleRoot, agentPath, "./internal/driver/codexprocess/testdata/fake-agent")
	buildFixture(t, moduleRoot, supervisorPath, "./cmd/wf-engine")

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("tracked content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "nested", "untracked.txt"), []byte("untracked content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := directoryDigest(t, workspace)

	stateRoot := t.TempDir()
	state := store.New(stateRoot)
	process := codexprocess.New(codexprocess.Config{StateRoot: stateRoot, Executable: agentPath, SupervisorExecutable: supervisorPath, Sandbox: "workspace-write", PollInterval: 10 * time.Millisecond})
	service := team.NewService(state)
	if err := service.SetDriver(codexprocess.NewExplorationAdapter(process)); err != nil {
		t.Fatal(err)
	}
	started, err := service.Start(context.Background(), teamcontract.TeamStartRequestV1{SchemaVersion: teamcontract.SchemaVersion, ClientRequestID: "integration-panel", Project: workspace, Topic: "Compare two recovery designs"})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := service.DispatchInitial(context.Background(), started.Team.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != teamcontract.LifecycleClosed || finished.CloseReason != teamcontract.ClosePanelSettled {
		t.Fatalf("finished Team=%+v", finished)
	}
	messages, err := state.ReadTeamMessages(finished.TeamID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	models := make(map[string]bool)
	for _, message := range messages {
		var contribution teamcontract.ContributionV1
		if err := teamcontract.DecodeStrict([]byte(message.Content), &contribution); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(contribution.ContentMarkdown, "fixture contribution from ") {
			t.Fatalf("contribution=%+v", contribution)
		}
		models[strings.TrimPrefix(contribution.ContentMarkdown, "fixture contribution from ")] = true
	}
	if !models["gpt-5.6"] || !models["gpt-5.6-luna"] {
		t.Fatalf("distinct model contributions=%v", models)
	}
	if after := directoryDigest(t, workspace); after != before {
		t.Fatalf("workspace changed: before=%s after=%s", before, after)
	}
	if ids, err := state.ListRunIDs(); err != nil || len(ids) != 0 {
		t.Fatalf("Panel created Runs=%v err=%v", ids, err)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "memory")); !os.IsNotExist(err) {
		t.Fatalf("Panel created M5 Memory state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "runs")); !os.IsNotExist(err) {
		t.Fatalf("Panel created Workflow state: %v", err)
	}
}

func TestOneRoundPanelPreservesSuccessfulContributionWhenPeerFails(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("process recovery is supported on Windows and Linux")
	}
	moduleRoot, fixtureDir := moduleDirectory(t), t.TempDir()
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	agentPath := filepath.Join(fixtureDir, "fake-codex"+extension)
	supervisorPath := filepath.Join(fixtureDir, "fishyume-engine"+extension)
	buildFixture(t, moduleRoot, agentPath, "./internal/driver/codexprocess/testdata/fake-agent")
	buildFixture(t, moduleRoot, supervisorPath, "./cmd/wf-engine")

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("unchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := directoryDigest(t, workspace)
	stateRoot := t.TempDir()
	state := store.New(stateRoot)
	process := codexprocess.New(codexprocess.Config{StateRoot: stateRoot, Executable: agentPath, SupervisorExecutable: supervisorPath, PollInterval: 10 * time.Millisecond})
	service := team.NewService(state)
	if err := service.SetDriver(codexprocess.NewExplorationAdapter(process)); err != nil {
		t.Fatal(err)
	}
	started, err := service.Start(context.Background(), teamcontract.TeamStartRequestV1{SchemaVersion: teamcontract.SchemaVersion, ClientRequestID: "integration-partial-panel", Project: workspace, Topic: "scenario:team-partial\nCompare two recovery designs"})
	if err != nil {
		t.Fatal(err)
	}
	finished, dispatchErr := service.DispatchInitial(context.Background(), started.Team.TeamID)
	if dispatchErr == nil {
		t.Fatal("partially failed panel unexpectedly returned no dispatch error")
	}
	if finished.State != teamcontract.LifecycleClosed || finished.CloseReason != teamcontract.ClosePanelSettled {
		t.Fatalf("finished Team=%+v", finished)
	}
	messages, err := state.ReadTeamMessages(finished.TeamID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("retained messages=%+v err=%v", messages, err)
	}
	var contribution teamcontract.ContributionV1
	if err := teamcontract.DecodeStrict([]byte(messages[0].Content), &contribution); err != nil || contribution.ContentMarkdown != "fixture contribution from gpt-5.6" {
		t.Fatalf("retained contribution=%+v err=%v", contribution, err)
	}
	turnIDs, err := state.ListTeamTurnIDs(finished.TeamID)
	if err != nil || len(turnIDs) != 2 {
		t.Fatalf("turn IDs=%v err=%v", turnIDs, err)
	}
	states := make(map[string]teamcontract.ParticipantTurnV1)
	for _, turnID := range turnIDs {
		var turn teamcontract.ParticipantTurnV1
		if err := state.ReadTeamTurn(finished.TeamID, turnID, &turn); err != nil {
			t.Fatal(err)
		}
		states[turn.ModelID] = turn
	}
	if states["codex/local/gpt-5.6"].State != teamcontract.TurnResponded {
		t.Fatalf("successful turn=%+v", states["codex/local/gpt-5.6"])
	}
	failed := states["codex/local/gpt-5.6-luna"]
	if failed.State != teamcontract.TurnFailed || failed.Diagnostic == "" {
		t.Fatalf("failed turn=%+v", failed)
	}
	if after := directoryDigest(t, workspace); after != before {
		t.Fatalf("workspace changed: before=%s after=%s", before, after)
	}
	if ids, err := state.ListRunIDs(); err != nil || len(ids) != 0 {
		t.Fatalf("partial Panel created Runs=%v err=%v", ids, err)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "memory")); !os.IsNotExist(err) {
		t.Fatalf("partial Panel created M5 Memory state: %v", err)
	}
}

func directoryDigest(t *testing.T, root string) string {
	t.Helper()
	paths := make([]string, 0)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		relative, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(struct {
			Path string `json:"path"`
			Data string `json:"data"`
		}{filepath.ToSlash(relative), hex.EncodeToString(data)})
		_, _ = hash.Write(encoded)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
