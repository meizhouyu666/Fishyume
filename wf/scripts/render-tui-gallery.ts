import {writeFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {dirname, resolve} from 'node:path';
import {canonicalVisualFixtures} from '../src/tui/fixtures.js';
import {actionableNodes, initialConsoleInteractionState} from '../src/tui/interaction.js';
import {renderRunText} from '../src/tui/presentation.js';

const here = dirname(fileURLToPath(import.meta.url));
const outputPath = resolve(here, '../../docs/fishyume-m3.3-canonical-gallery.txt');
const blocks: string[] = [
  'Fishyume M3.3 Calm Operator Console — canonical text gallery',
  'Generated from pure RunStatusView fixtures; elapsed time is fixed at 02:18.',
  'Color capability is verified separately by tests; this file intentionally contains no ANSI escapes.',
];

for (const fixture of canonicalVisualFixtures) {
  const run = fixture.view.run!; const selectedIndex = Math.max(0, run.topologicalOrder.indexOf(fixture.selectedNodeId));
  const interaction = fixture.interaction ?? {...initialConsoleInteractionState, selectedIndex, selectedNodeId: fixture.selectedNodeId};
  for (const width of [80, 120, 160]) {
    blocks.push('', `${'='.repeat(24)} ${fixture.id} / ${width} columns ${'='.repeat(24)}`,
      renderRunText(fixture.view, width, 138_000, {
        selectedNodeId: fixture.selectedNodeId,
        detailExpanded: true,
        symbolMode: 'unicode',
        interactive: true,
        action: {interaction, actionable: actionableNodes(fixture.view), pending: false},
      }));
  }
}

await writeFile(outputPath, `${blocks.join('\n')}\n`, 'utf8');
process.stdout.write(`${outputPath}\n`);
