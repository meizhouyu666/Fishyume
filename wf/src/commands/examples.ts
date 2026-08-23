import {readFile} from 'node:fs/promises';
import {Command, Option} from 'clipanion';

interface ProductExample {
  name: string;
  filename: string;
  description: string;
}

export const productExamples: readonly ProductExample[] = [
  {
    name: 'repository-hardening',
    filename: 'repository-hardening.yaml',
    description: '并行仓库审计、方案审批、集中实施、独立验证与最终验收',
  },
];

export function examplesListText(): string {
  return [
    'Fishyume product workflow examples',
    '',
    ...productExamples.map(example => `${example.name}\n  ${example.description}`),
    '',
    '查看模板：fishyume examples show <name>',
  ].join('\n') + '\n';
}

export async function loadProductExample(name: string): Promise<string> {
  const example = productExamples.find(candidate => candidate.name === name);
  if (!example) throw new Error(`unknown example ${JSON.stringify(name)}; run fishyume examples list`);

  const candidates = [
    new URL(`../examples/${example.filename}`, import.meta.url),
    new URL(`../../../docs/examples/${example.filename}`, import.meta.url),
  ];
  for (const candidate of candidates) {
    try {return await readFile(candidate, 'utf8')}
    catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error;
    }
  }
  throw new Error(`bundled example ${JSON.stringify(example.name)} is missing`);
}

export class ExamplesListCommand extends Command {
  static paths = [['examples', 'list']];
  static usage = Command.Usage({description: 'List bundled product Workflow examples.'});
  async execute(): Promise<number> {
    this.context.stdout.write(examplesListText());
    return 0;
  }
}

export class ExamplesShowCommand extends Command {
  static paths = [['examples', 'show']];
  static usage = Command.Usage({description: 'Print one bundled Workflow YAML to stdout without starting the Engine.'});
  name = Option.String({required: true, name: 'name'});
  async execute(): Promise<number> {
    try {
      this.context.stdout.write(await loadProductExample(this.name));
      return 0;
    } catch (error) {
      this.context.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
      return 6;
    }
  }
}
