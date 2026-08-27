import type {Contribution, TeamMessage} from '../../../wf/src/bridge/team.js';

/** Formats the untrusted public contribution payload for a compact message view. */
export function messageContent(message: TeamMessage): string {
  if (message.kind === 'host_message') return message.content;
  try {
    const contribution = JSON.parse(message.content) as Contribution;
    if (contribution.contentMarkdown) return contribution.contentMarkdown;
    if (contribution.resultType && contribution.output !== undefined) {
      const rendered = typeof contribution.output === 'string' ? contribution.output : JSON.stringify(contribution.output, null, 2);
      return `[${contribution.resultType}]\n${rendered}`;
    }
    return message.content;
  } catch {
    return message.content;
  }
}
