import React, {type ReactNode} from 'react';
import {Box, Text} from 'ink';
import {colorFor, type ColorMode, type ColorRole} from './design-tokens.js';
import {dividerLine, type RunTextPresentation, type WorkflowRowPresentation} from './presentation.js';

interface ThemeProps {colorMode: ColorMode}
function ThemedText({role, colorMode, children, bold}: ThemeProps & {role: ColorRole; children: ReactNode; bold?: boolean}) {
  return <Text color={colorFor(role, colorMode)} bold={bold}>{children}</Text>;
}

function WorkflowRow({row, colorMode}: ThemeProps & {row: WorkflowRowPresentation}) {
  return <Box>
    <ThemedText role={row.selected ? 'brand' : 'muted'} colorMode={colorMode} bold={row.selected}>{`${row.marker} `}</ThemedText>
    <ThemedText role={row.role} colorMode={colorMode} bold={row.selected}>{`${row.statusText} `}</ThemedText>
    <Text bold={row.selected}>{row.content}</Text>
  </Box>;
}

export function CalmConsole({presentation, width, colorMode, symbolMode}: ThemeProps & {presentation: RunTextPresentation; width: number; symbolMode: 'unicode' | 'ascii'}) {
  return <Box flexDirection="column" width={width}>
    {presentation.header.map((line, index) => <ThemedText key={`header:${index}`} role={index === 0 ? 'brand' : index === 1 ? 'strong' : 'muted'} colorMode={colorMode} bold={index === 0}>{line}</ThemedText>)}
    <ThemedText role="muted" colorMode={colorMode}>{presentation.divider}</ThemedText>
    {presentation.workflow.map(row => <WorkflowRow key={row.nodeId} row={row} colorMode={colorMode}/>)}
    {presentation.detail ? <>
      <ThemedText role={presentation.detail.role} colorMode={colorMode} bold>{dividerLine(width, symbolMode, presentation.detail.title)}</ThemedText>
      {presentation.detail.lines.map((line, index) => <Text key={`detail:${index}`}>{line}</Text>)}
      <ThemedText role="muted" colorMode={colorMode}>{presentation.divider}</ThemedText>
    </> : null}
    {presentation.statusStrip ? <ThemedText role="muted" colorMode={colorMode}>{presentation.statusStrip}</ThemedText> : null}
    {presentation.footer.map((line, index) => <ThemedText key={`footer:${index}`} role="muted" colorMode={colorMode}>{line}</ThemedText>)}
  </Box>;
}
