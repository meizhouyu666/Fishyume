export type TerminalSize = 'narrow' | 'medium' | 'wide';
export function terminalSize(width: number): TerminalSize {if (width < 100) return 'narrow'; if (width < 140) return 'medium'; return 'wide'}
function isCombining(codePoint: number): boolean {return (codePoint >= 0x0300 && codePoint <= 0x036f) || (codePoint >= 0x1ab0 && codePoint <= 0x1aff) || (codePoint >= 0x1dc0 && codePoint <= 0x1dff) || (codePoint >= 0xfe20 && codePoint <= 0xfe2f)}
function isWide(codePoint: number): boolean {return codePoint >= 0x1100 && (codePoint <= 0x115f || codePoint === 0x2329 || codePoint === 0x232a || (codePoint >= 0x2e80 && codePoint <= 0xa4cf && codePoint !== 0x303f) || (codePoint >= 0xac00 && codePoint <= 0xd7a3) || (codePoint >= 0xf900 && codePoint <= 0xfaff) || (codePoint >= 0xfe10 && codePoint <= 0xfe19) || (codePoint >= 0xfe30 && codePoint <= 0xfe6f) || (codePoint >= 0xff00 && codePoint <= 0xff60) || (codePoint >= 0xffe0 && codePoint <= 0xffe6) || (codePoint >= 0x1f300 && codePoint <= 0x1faff) || (codePoint >= 0x20000 && codePoint <= 0x3fffd))}
export function displayWidth(text: string): number {let width = 0; for (const character of text) {const codePoint = character.codePointAt(0) ?? 0; if (codePoint === 0 || codePoint < 0x20 || (codePoint >= 0x7f && codePoint < 0xa0) || isCombining(codePoint)) continue; width += isWide(codePoint) ? 2 : 1} return width}
export function normalizeInline(text: string): string {return text.replace(/\s+/g, ' ').trim()}
export function fitText(text: string, width: number): string {if (width <= 0) return ''; const normalized = normalizeInline(text); if (displayWidth(normalized) <= width) return normalized; if (width === 1) return '…'; let result = ''; let used = 0; for (const character of normalized) {const characterWidth = displayWidth(character); if (used + characterWidth > width - 1) break; result += character; used += characterWidth} return `${result}…`}
export function padDisplay(text: string, width: number): string {const fitted = fitText(text, width); return `${fitted}${' '.repeat(Math.max(0, width - displayWidth(fitted)))}`}
export function assertWidth(lines: readonly string[], width: number): boolean {return lines.every(line => displayWidth(line) <= width)}

export function joinColumns(left: string, right: string, width: number, minimumGap = 2): string {
  if (width <= 0) return '';
  const normalizedLeft = normalizeInline(left); const normalizedRight = normalizeInline(right);
  const rightWidth = displayWidth(normalizedRight);
  if (rightWidth >= width) return fitText(normalizedRight, width);
  const leftWidth = Math.max(0, width - rightWidth - minimumGap);
  if (leftWidth <= 0) return fitText(`${normalizedLeft} ${normalizedRight}`, width);
  const fittedLeft = fitText(normalizedLeft, leftWidth);
  return `${fittedLeft}${' '.repeat(Math.max(minimumGap, width - displayWidth(fittedLeft) - rightWidth))}${normalizedRight}`;
}

export function wrapItems(items: readonly string[], width: number, separator = '  '): string[] {
  const result: string[] = []; let line = '';
  for (const rawItem of items) {
    const item = normalizeInline(rawItem);
    if (!item) continue;
    if (!line) {line = fitText(item, width); continue}
    const candidate = `${line}${separator}${item}`;
    if (displayWidth(candidate) <= width) line = candidate;
    else {result.push(line); line = fitText(item, width)}
  }
  if (line) result.push(line);
  return result;
}
