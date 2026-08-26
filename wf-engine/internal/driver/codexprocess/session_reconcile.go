package codexprocess

import (
	"context"
	"fmt"
	"time"

	"wf.local/wf-engine/internal/sessiondriver"
)

func (a *SessionAdapter) reconcileTurn(ctx context.Context, record sessionRecord) (sessionRecord, error) {
	if record.LastTurnID == "" {
		return record, nil
	}
	client := a.client(record.HandleID)
	if client == nil {
		var err error
		client, err = a.connectAndResume(ctx, record)
		if err != nil {
			if isMissingThreadError(err) {
				record.State = sessionLost
				record.LastTurnState = sessiondriver.TurnLost
				record.LastTurnOutput = ""
				record.LastTurnDiagnostic = boundedSessionDiagnostic(err.Error())
				record.Revision++
				record.UpdatedAt = time.Now().UTC()
				_ = a.writeRecord(record)
				return record, nil
			}
			return record, err
		}
	}
	var read appThreadReadResponse
	if err := client.request(ctx, "thread/read", map[string]any{"threadId": record.ThreadID, "includeTurns": true}, &read); err != nil {
		if isTransientThreadReadError(err) {
			return record, nil
		}
		return record, err
	}
	if read.Thread.ID != record.ThreadID || read.Thread.SessionID != record.ExternalSessionID || !samePath(read.Thread.CWD, record.Workspace) {
		return record, sessiondriver.Conflict("Codex thread/read identity or workspace changed")
	}
	external := findAppTurn(read.Thread.Turns, record.LastExternalTurnID, record.LastTurnID)
	if external == nil {
		if record.LastTurnState == sessiondriver.TurnDispatching {
			record.State = sessionLost
			record.LastTurnState = sessiondriver.TurnLost
			record.LastTurnDiagnostic = "persisted turn launch intent has no matching Codex turn"
			record.Revision++
			record.UpdatedAt = time.Now().UTC()
			if err := a.writeRecord(record); err != nil {
				return record, err
			}
			return record, nil
		}
		return record, sessiondriver.Lost("Codex turn %q is missing", record.LastExternalTurnID)
	}
	state, output, diagnostic := mapAppTurn(*external, record.LastTurnMaxOutputBytes)
	changed := record.LastExternalTurnID != external.ID || record.LastTurnState != state || record.LastTurnOutput != output || record.LastTurnDiagnostic != diagnostic
	if !changed {
		return record, nil
	}
	record.LastExternalTurnID = external.ID
	record.LastTurnState = state
	record.LastTurnOutput = output
	record.LastTurnDiagnostic = diagnostic
	if state == sessiondriver.TurnLost {
		record.State = sessionLost
	}
	record.Revision++
	record.UpdatedAt = time.Now().UTC()
	if err := a.writeRecord(record); err != nil {
		return record, err
	}
	return record, nil
}

func (a *SessionAdapter) connectAndResume(ctx context.Context, record sessionRecord) (*appServerClient, error) {
	discovered, err := a.process.discoverExecutable()
	if err != nil {
		return nil, err
	}
	if !sameExecutable(discovered.Path, record.Executable) || discovered.SHA256 != record.ExecutableSHA256 {
		return nil, sessiondriver.Conflict("Codex executable identity changed since the session started")
	}
	client, err := startAppServer(ctx, record.Executable, a.process.config.StateRoot, a.process.config.MaxStderrBytes)
	if err != nil {
		return nil, err
	}
	var resumed appThreadResponse
	err = client.request(ctx, "thread/resume", map[string]any{
		"threadId": record.ThreadID, "cwd": record.Workspace, "model": modelName(record.ModelID),
		"sandbox": "read-only", "approvalPolicy": "never",
	}, &resumed)
	if err != nil {
		_ = client.close()
		return nil, err
	}
	if err := validateThreadResponse(resumed, record.Workspace, record.ModelID); err != nil {
		_ = client.close()
		return nil, err
	}
	if resumed.Thread.ID != record.ThreadID || resumed.Thread.SessionID != record.ExternalSessionID {
		_ = client.close()
		return nil, sessiondriver.Conflict("Codex resumed a different thread or session identity")
	}
	a.setClient(record.HandleID, client)
	return client, nil
}

func findAppTurn(turns []appTurn, externalID, logicalID string) *appTurn {
	for index := range turns {
		turn := &turns[index]
		if externalID != "" && turn.ID == externalID {
			return turn
		}
		if externalID == "" {
			for _, item := range turn.Items {
				if item.Type == "userMessage" && item.ClientID == logicalID {
					return turn
				}
			}
		}
	}
	return nil
}

func mapAppTurn(turn appTurn, maxBytes int) (sessiondriver.TurnState, string, string) {
	switch turn.Status {
	case "inProgress":
		return sessiondriver.TurnActive, "", ""
	case "interrupted":
		return sessiondriver.TurnInterrupted, "", ""
	case "failed":
		diagnostic := "Codex turn failed"
		if turn.Error != nil && turn.Error.Message != "" {
			diagnostic = turn.Error.Message
		}
		return sessiondriver.TurnFailed, "", boundedSessionDiagnostic(diagnostic)
	case "completed":
		output := ""
		for _, item := range turn.Items {
			if item.Type == "agentMessage" {
				output = item.Text
			}
		}
		if output == "" {
			return sessiondriver.TurnFailed, "", "Codex completed without an agent message"
		}
		if len([]byte(output)) > maxBytes {
			return sessiondriver.TurnFailed, "", fmt.Sprintf("Codex response exceeds %d bytes", maxBytes)
		}
		encoded, err := encodeSessionContribution(output)
		if err != nil {
			return sessiondriver.TurnFailed, "", boundedSessionDiagnostic(err.Error())
		}
		return sessiondriver.TurnResponded, encoded, ""
	default:
		return sessiondriver.TurnLost, "", boundedSessionDiagnostic("unsupported Codex turn status " + turn.Status)
	}
}
