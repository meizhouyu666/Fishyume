# Team Structured Contributions

Team participant messages use a versioned contribution envelope. Completion is
defined by `status`, while the result representation is selected independently.

## Envelope

```json
{
  "schemaVersion": "fishyume.team/v1",
  "status": "completed",
  "resultType": "decision",
  "output": {"decision": "adopt option A", "rationale": ["bounded scope"]},
  "warnings": [],
  "openQuestions": []
}
```

`resultType` is one of `report`, `decision`, `artifact`, `data`, or `question`.
`output` is valid, bounded JSON and is rendered by clients according to its
type. The first release intentionally keeps the payload shape extensible; the
type is the stable dispatch key and clients must safely display unknown fields.

For compatibility, an envelope may instead contain the legacy
`contentMarkdown` string. Existing persisted messages remain valid, and plain
text returned by a driver is still wrapped in this legacy field. New producers
should prefer `resultType` and `output`; they may include `contentMarkdown` as a
human-readable fallback during migration.

Handoff artifacts continue to reference the original message IDs and hashes.
They do not duplicate or reinterpret the contribution payload.
