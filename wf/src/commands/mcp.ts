import {Command} from 'clipanion';
import {runMCP} from '../mcp/server.js';

export class MCPCommand extends Command {
  static paths = [['mcp']];
  static usage = Command.Usage({description: 'Serve the Agent-facing Application API as MCP tools over stdio.'});
  async execute(): Promise<number> {await runMCP(); return 0}
}
