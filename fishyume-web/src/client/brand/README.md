# Harness brand assets

The active member badges use the directly imported, tree-shaken components
from `@lobehub/icons` (`Claude.Color` for the Claude petal mark, `Color` for
other providers; `Mono` for
OpenCode and the OpenAI mark used for the Codex harness). This avoids a
runtime image request and keeps the icon vectors in the client bundle.

These files are bundled into the native DSH client. They are not loaded from
remote URLs at runtime.

| File | Harness | Official source |
| --- | --- | --- |
| `claude.svg` | Claude | https://claude.ai/favicon.svg |
| `opencode.svg` | OpenCode | https://raw.githubusercontent.com/sst/opencode/dev/packages/console/app/src/asset/brand/opencode-logo-dark-square.svg |
| `deepseek.ico` | DeepSeek | https://www.deepseek.com/favicon.ico |

The PNG/ICO/SVG files in this directory are retained as previously downloaded
fallback references; they are no longer imported by the client.
