import {randomUUID} from 'node:crypto';
import {Command, Option} from 'clipanion';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import {callRouting, configApiVersion, RoutingCallError, type EffectiveCatalogResponse} from '../bridge/routing.js';

type Writer = {write(text: string): unknown};

async function routingCall(client: EngineClient, operation: () => Promise<unknown>, output: Writer): Promise<number> {
  try {output.write(`${JSON.stringify(await operation(), null, 2)}\n`); return 0}
  catch (error) {
    const detail = error instanceof RoutingCallError ? error.routingError : {code: 'internal', message: error instanceof Error ? error.message : String(error)};
    output.write(`${JSON.stringify({error: detail})}\n`); return 6;
  } finally {await client.close()}
}

export class DriversInspectCommand extends Command {
  static paths = [['drivers', 'inspect']];
  static usage = Command.Usage({description: 'Inspect Fishyume Drivers and the cached Codex model discovery state.'});
  refresh = Option.Boolean('--refresh', false, {description: 'Refresh models through Codex model/list before printing'});
  async execute(): Promise<number> {
    const client = new EngineBridge();
    return routingCall(client, async () => {
      if (this.refresh) {
        await callRouting(client, 'team.routes.refresh', {schemaVersion: configApiVersion});
        await callRouting(client, 'driver.models.discover', {schemaVersion: configApiVersion});
      }
      return callRouting(client, 'driver.list', {schemaVersion: configApiVersion});
    }, this.context.stdout);
  }
}

export class RoutingShowCommand extends Command {
  static paths = [['routing', 'show']];
  static usage = Command.Usage({description: 'Show the effective Workflow Catalog and each Codex route state.'});
  async execute(): Promise<number> {const client = new EngineBridge(); return routingCall(client, () => callRouting(client, 'routing.catalog.effective', {schemaVersion: configApiVersion}), this.context.stdout)}
}

abstract class RoutingToggleCommand extends Command {
  route = Option.String({required: true, name: 'route'});
  abstract enabled: boolean;
  async execute(): Promise<number> {
    const client = new EngineBridge();
    return routingCall(client, async () => {
      const current = await callRouting(client, 'routing.config.get', {schemaVersion: configApiVersion});
      return callRouting(client, 'routing.config.update', {schemaVersion: configApiVersion, mutationId: `routing-${randomUUID()}`, expectedRevision: current.config.revision, routeId: normalizeRoute(this.route), enabled: this.enabled});
    }, this.context.stdout);
  }
}

export class RoutingEnableCommand extends RoutingToggleCommand {
  static paths = [['routing', 'enable']];
  static usage = Command.Usage({description: 'Enable one product-qualified Codex route.'});
  enabled = true;
}

export class RoutingDisableCommand extends RoutingToggleCommand {
  static paths = [['routing', 'disable']];
  static usage = Command.Usage({description: 'Disable one product-qualified Codex route.'});
  enabled = false;
}

export class RoutingRefreshCommand extends Command {
  static paths = [['routing', 'refresh']];
  static usage = Command.Usage({description: 'Refresh Codex model discovery and optionally run paid active connectivity probes.'});
  probe = Option.Boolean('--probe', false, {description: 'Actively execute each enabled discovered model'});
  async execute(): Promise<number> {
    const client = new EngineBridge();
    return routingCall(client, async () => {
      const discovery = await callRouting(client, 'driver.models.discover', {schemaVersion: configApiVersion});
      if (this.probe) await callRouting(client, 'driver.models.probe', {schemaVersion: configApiVersion});
      const effective = await callRouting(client, 'routing.catalog.effective', {schemaVersion: configApiVersion}) as EffectiveCatalogResponse;
      return {schemaVersion: configApiVersion, discovery, effective};
    }, this.context.stdout);
  }
}

function normalizeRoute(value: string): string {return value.includes('/') ? value : `codex/local/${value}`}

export class TeamRoutesShowCommand extends Command {
  static paths = [['team', 'routes']];
  static usage = Command.Usage({description: 'Show persistent Team Agent routes and local Driver readiness.'});
  async execute(): Promise<number> {const client = new EngineBridge(); return routingCall(client, () => callRouting(client, 'team.routes.get', {schemaVersion: configApiVersion}), this.context.stdout)}
}

export class TeamRoutesRefreshCommand extends Command {
  static paths = [['team', 'routes', 'refresh']];
  static usage = Command.Usage({description: 'Rediscover installed Agent CLIs and apply effective Team routes without model calls.'});
  async execute(): Promise<number> {const client = new EngineBridge(); return routingCall(client, () => callRouting(client, 'team.routes.refresh', {schemaVersion: configApiVersion}), this.context.stdout)}
}

export class TeamRoutesSetCommand extends Command {
  static paths = [['team', 'routes', 'set']];
  static usage = Command.Usage({description: 'Persist one trusted Team Agent route. Use model default to inherit the Agent configuration.'});
  routeId = Option.String('--route', {required: true});
  driver = Option.String('--driver', {required: true});
  provider = Option.String('--provider', {required: true});
  model = Option.String('--model', {required: true});
  disabled = Option.Boolean('--disabled', false);
  async execute(): Promise<number> {
    if (!['codex', 'claude', 'opencode'].includes(this.driver)) {this.context.stdout.write('{"error":{"code":"invalid_argument","message":"driver must be codex, claude, or opencode"}}\n'); return 6}
    const client = new EngineBridge();
    return routingCall(client, async () => {
      const current = await callRouting(client, 'team.routes.get', {schemaVersion: configApiVersion});
      return callRouting(client, 'team.routes.upsert', {schemaVersion: configApiVersion, mutationId: `team-route-${randomUUID()}`, expectedRevision: current.config.revision, routeId: this.routeId, driver: this.driver, provider: this.provider, model: this.model, enabled: !this.disabled});
    }, this.context.stdout);
  }
}

export class TeamRoutesRemoveCommand extends Command {
  static paths = [['team', 'routes', 'remove']];
  static usage = Command.Usage({description: 'Remove one user-defined Team Agent route.'});
  routeId = Option.String({required: true, name: 'route'});
  async execute(): Promise<number> {
    const client = new EngineBridge();
    return routingCall(client, async () => {
      const current = await callRouting(client, 'team.routes.get', {schemaVersion: configApiVersion});
      return callRouting(client, 'team.routes.remove', {schemaVersion: configApiVersion, mutationId: `team-route-${randomUUID()}`, expectedRevision: current.config.revision, routeId: this.routeId});
    }, this.context.stdout);
  }
}
