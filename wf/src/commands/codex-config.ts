import {readFileSync} from 'node:fs';
import {chmod, mkdir, readFile, rename, stat, writeFile} from 'node:fs/promises';
import {homedir} from 'node:os';
import {dirname, join} from 'node:path';

export const fishyumeMcpTools = [
  'system.capabilities',
  'routing.catalog',
  'workflow.validate',
  'workflow.explain',
  'run.start',
  'run.list',
  'run.get',
  'run.events',
  'run.action',
  'run.result',
  'memory.create',
  'memory.get',
  'memory.list',
  'memory.supersede',
  'memory.delete',
  'team.capabilities',
  'team.start',
  'team.list',
  'team.get',
  'team.events',
  'team.messages',
  'team.action',
  'team.handoff.create',
  'team.handoff.get',
  'team.handoff.list',
  'team.handoff.bindRun',
  'web.open',
  'driver.list',
  'driver.models.discover',
  'driver.models.probe',
  'driver.inventory',
  'routing.config.get',
  'routing.config.update',
  'routing.availability',
  'routing.catalog.effective',
  'team.routes.get',
  'team.routes.refresh',
  'team.routes.upsert',
  'team.routes.remove',
] as const;

export function codexConfigPath(environment: NodeJS.ProcessEnv = process.env): string {
  return join(environment.CODEX_HOME || join(homedir(), '.codex'), 'config.toml');
}

export function withFishyumeApprovalPolicy(content: string): string {
  const newline = content.includes('\r\n') ? '\r\n' : '\n';
  const lines = content.replaceAll('\r\n', '\n').split('\n');
  const rootIndex = lines.findIndex(line => line.trim() === '[mcp_servers.fishyume]');
  if (rootIndex < 0) throw new Error('Codex did not create [mcp_servers.fishyume]');
  let sectionEnd = lines.length;
  for (let index = rootIndex + 1; index < lines.length; index++) {
    const header = lines[index].trim();
    if (/^\[.+\]$/.test(header) && !header.startsWith('[mcp_servers.fishyume.')) {sectionEnd = index; break}
  }

  const section = lines.slice(rootIndex, sectionEnd);
  const rootEnd = section.findIndex((line, index) => index > 0 && /^\[.+\]$/.test(line.trim()));
  const rootLines = section.slice(0, rootEnd < 0 ? section.length : rootEnd)
    .filter(line => !/^\s*(?:required|default_tools_approval_mode)\s*=/.test(line));
  while (rootLines.at(-1)?.trim() === '') rootLines.pop();
  rootLines.push('required = true', 'default_tools_approval_mode = "approve"', '');

  const retained: string[] = [];
  for (let index = rootEnd < 0 ? section.length : rootEnd; index < section.length;) {
    const next = index + 1;
    let end = next;
    while (end < section.length && !/^\[.+\]$/.test(section[end].trim())) end++;
    if (!section[index].trim().startsWith('[mcp_servers.fishyume.tools.')) retained.push(...section.slice(index, end));
    index = end;
  }
  while (retained.at(-1)?.trim() === '') retained.pop();
  if (retained.length) retained.push('');
  for (const tool of fishyumeMcpTools) retained.push(`[mcp_servers.fishyume.tools.${JSON.stringify(tool)}]`, 'approval_mode = "approve"');

  const replacement = [...rootLines, ...retained, ''];
  return [...lines.slice(0, rootIndex), ...replacement, ...lines.slice(sectionEnd)].join(newline).replace(new RegExp(`${newline}{3,}`, 'g'), `${newline}${newline}`);
}

export function hasFishyumeApprovalPolicy(path = codexConfigPath()): boolean {
  try {
    const content = readFileSync(path, 'utf8');
    return withFishyumeApprovalPolicy(content) === content;
  } catch {return false}
}

export async function applyCodexMcpApprovalPolicy(path = codexConfigPath()): Promise<void> {
  const original = await readFile(path, 'utf8');
  const updated = withFishyumeApprovalPolicy(original);
  if (updated === original) return;
  const metadata = await stat(path);
  const temporary = `${path}.fishyume-${process.pid}.tmp`;
  await mkdir(dirname(path), {recursive: true});
  await writeFile(temporary, updated, {encoding: 'utf8', mode: metadata.mode});
  await chmod(temporary, metadata.mode);
  await rename(temporary, path);
}
