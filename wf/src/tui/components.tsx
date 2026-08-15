import React, {type ReactNode} from 'react';
import {Box, Text} from 'ink';
import {colorFor, type ColorMode, type ColorRole} from './design-tokens.js';
import {dividerLine, type HeaderLinePresentation, type RunTextPresentation, type StyledTextSegment, type WorkflowRowPresentation} from './presentation.js';

interface ThemeProps {colorMode: ColorMode}
function ThemedText({role, colorMode, children, bold}: ThemeProps & {role: ColorRole; children: ReactNode; bold?: boolean}) {
  return <Text color={colorFor(role, colorMode)} bold={bold}>{children}</Text>;
}

function StyledSegment({segment, colorMode}: ThemeProps & {segment: StyledTextSegment}) {
  return segment.role
    ? <ThemedText role={segment.role} colorMode={colorMode} bold={segment.bold}>{segment.text}</ThemedText>
    : <Text bold={segment.bold}>{segment.text}</Text>;
}

function HeaderLine({line, colorMode}: ThemeProps & {line: HeaderLinePresentation}) {
  return <Box>{line.segments.map((segment, index) => <StyledSegment key={index} segment={segment} colorMode={colorMode}/>)}</Box>;
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
    {presentation.header.map((line, index) => <HeaderLine key={`header:${index}`} line={line} colorMode={colorMode}/>)}
    <ThemedText role="muted" colorMode={colorMode}>{presentation.divider}</ThemedText>
    {presentation.attention ? <>
      {presentation.attention.lines.map((line, index) => <ThemedText key={`attention:${index}`} role={presentation.attention!.role} colorMode={colorMode} bold>{line}</ThemedText>)}
      <ThemedText role="muted" colorMode={colorMode}>{presentation.divider}</ThemedText>
    </> : null}
    {presentation.workflow.map(row => <WorkflowRow key={row.nodeId} row={row} colorMode={colorMode}/>)}
    {presentation.detail ? <>
      <ThemedText role={presentation.detail.role} colorMode={colorMode} bold>{dividerLine(width, symbolMode, presentation.detail.title)}</ThemedText>
      {presentation.detail.lines.map((line, index) => <Text key={`detail:${index}`}>{line}</Text>)}
      <ThemedText role="muted" colorMode={colorMode}>{presentation.divider}</ThemedText>
    </> : null}
    {presentation.statusStrip ? <Text>{presentation.statusStrip}</Text> : null}
    {presentation.footer.map((line, index) => <Text key={`footer:${index}`}>{line}</Text>)}
  </Box>;
}
