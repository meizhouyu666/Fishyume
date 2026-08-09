import {Command} from 'clipanion';
import {runMCP} from '../mcp/server.js';

export class MCPCommand extends Command {
  static paths = [['mcp']];
  async execute(): Promise<number> {await runMCP(); return 0}
}
