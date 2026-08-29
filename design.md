# Fishyume UI Design System

This is the visual baseline for Fishyume pages inside DSH. Read this file before adding or revising a page. The target is a Linear-inspired workbench: dark neutral surfaces, restrained color, clear hierarchy, compact navigation, and information-dense workflow views.

The baseline was checked against the public Linear Method page and its shipped web CSS on 2026-08-29. The screenshots in `output/` are the primary product reference; Linear is used to validate the underlying system of tokens, typography, and control sizing.

## Color

Use neutral surfaces for structure and reserve saturated colors for state, selection, and primary actions.

| Token | Value | Use |
| --- | --- | --- |
| `--fy-bg-canvas` | `#08090a` | Main page background |
| `--fy-bg-surface` | `#0f1011` | Header, navigation, list background |
| `--fy-bg-elevated` | `#1c1c1f` | Selected rows, cards, menus |
| `--fy-bg-hover` | `#232326` | Hover and keyboard-focus surface |
| `--fy-border` | `#28282c` | Default 1px border |
| `--fy-border-strong` | `#3e3e44` | Selected/focused border |
| `--fy-text-primary` | `#f7f8f8` | Titles and important values |
| `--fy-text-secondary` | `#8a8f98` | Supporting text and metadata |
| `--fy-text-tertiary` | `#62666d` | Placeholders, timestamps, quiet labels |
| `--fy-accent` | `#5e6ad2` | Active navigation and primary action |
| `--fy-accent-soft` | `#828fff` | Accent text on dark surfaces |
| `--fy-success` | `#27a644` | Completed/success state |
| `--fy-warning` | `#f0bf00` | Waiting/attention state |
| `--fy-danger` | `#eb5757` | Failed/destructive state |
| `--fy-info` | `#4ea7fc` | Running/information state |

Do not introduce gradients, glowing backgrounds, or a new saturated brand color for individual pages. Fishyume's cyan logo is an identity asset, not the page color system.

## Typography

Use `Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`. Use the system CJK fallback naturally; do not load a decorative display face for product UI.

| Role | Size | Weight | Line height | Use |
| --- | ---: | ---: | ---: | --- |
| Page title | `20px` | `650` | `28px` | Current workspace/page name |
| Section title | `15px` | `600` | `22px` | Workflow, timeline, handoff headings |
| Body | `14px` | `400` | `21px` | Descriptions and readable content |
| List title | `13px` | `600` | `18px` | Team/run/node names |
| Metadata | `12px` | `400` | `17px` | IDs, timestamps, driver and project info |
| Eyebrow/label | `11px` | `600` | `16px` | Context labels and compact chips |

Use weight and contrast before increasing font size. Titles should be bright; metadata should remain visibly quieter. Avoid all-caps labels except short technical identifiers such as `FISHYUME`.

## Spacing

Use a 4px base grid. Common values:

- `4px`: icon-to-label gaps and dense status spacing.
- `8px`: row padding, chip gaps, compact controls.
- `12px`: card padding and list item padding.
- `16px`: section spacing and content gutters.
- `20px`: detail-pane padding.
- `24px`: major section separation.
- `32px`: page-level breathing room on wide layouts.

Keep content aligned to a shared left edge. Prefer a little extra vertical space between sections over large empty card margins. On narrow screens, collapse columns into a stacked list/detail flow and keep horizontal workflow timelines scrollable.

## Components

### Buttons and controls

- Default radius: `6px`; icon-only controls may use `6px` or a circular hit area when the icon is familiar.
- Height: `32px` for normal controls, `28px` for compact controls.
- Default surface is transparent with a `1px solid var(--fy-border)` border.
- Hover uses `var(--fy-bg-hover)`; active/selected uses `var(--fy-bg-elevated)` and `var(--fy-border-strong)`.
- Primary actions use `var(--fy-accent)` with `#ffffff` text; do not add a shadow.
- Every icon-only button needs an accessible label and tooltip/title.

### Cards and list rows

- Radius: `8px` maximum.
- Border: `1px solid var(--fy-border)`; use a stronger border only for selection or focus.
- Shadow: none by default. Use `0 8px 24px rgba(0, 0, 0, .22)` only for menus or floating overlays.
- Use rows for repeated teams/runs. Use cards for an individual workflow node or a genuinely framed detail block.
- Do not nest cards inside cards.

### Navigation and layout

- The DSH sidebar remains the global navigation and Fishyume adds one compact entry.
- Fishyume content uses a 64px context header, a 48px tab row, then a two-column list/detail workbench.
- List column: `minmax(220px, 0.8fr)`; detail column: `minmax(0, 1.6fr)`.
- Use 1px separators instead of heavy panels. Keep the canvas visually continuous.
- Workflow nodes are horizontal, scrollable, and connected with subtle arrows; node status is communicated with a colored left rule and text, not a full saturated card.

### States

Map `running` to info blue, `waiting` to warning yellow, `completed/succeeded` to success green, and `failed` to danger pink/red. Keep the rest of the node neutral. Empty, loading, and error states share the same centered quiet treatment and use one clear action.

### Information architecture

- Team pages use a list/detail workbench. The detail view must expose status, project, member roster, handoffs, and recent activity rather than only a title.
- Team settings is a first-class view for member roster and team execution metadata. Keep operational settings close to the selected team.
- Workflow views use progressive disclosure: compact nodes show identity and state; selecting a node reveals dependencies, attempt data, result, artifacts, warnings, and diagnostics.
- Event timelines are ordered by sequence, show timestamps, and are interactive. Selecting an event highlights its row and focuses the associated node when one exists.
- Prefer one clear detail surface over nested modal dialogs for routine inspection.

## Page checklist

Before shipping a new page:

1. Read this file and reuse the tokens above.
2. Verify the hierarchy at desktop and narrow widths.
3. Check that labels, buttons, and dynamic text fit without overlap.
4. Keep interaction states visible and keyboard-focusable.
5. Add or update a focused test when a page changes behavior.
