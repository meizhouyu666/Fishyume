import {EngineRpcError, type EngineClient} from './engine.js';
import type {RoutingCatalog} from './application.js';

export const configApiVersion = 'fishyume.config/v1' as const;
export const routingMethods = [
  'driver.list', 'driver.models.discover', 'driver.models.probe',
  'routing.config.get', 'routing.config.update', 'routing.availability', 'routing.catalog.effective',
] as const;
export type RoutingMethod = typeof routingMethods[number];
export type AvailabilityStatus = 'available' | 'unavailable' | 'unknown';

export interface ModelInfo {id: string; model: string; displayName: string; description?: string; hidden: boolean; default: boolean; supportedReasoningEfforts: string[]; defaultReasoningEffort: string; inputModalities?: string[]; serviceTiers?: string[]; defaultServiceTier?: string; supportsPersonality: boolean; supportsMultiAgentMode: boolean}
export interface ProductProfile {routeId: string; model: string; qualified: boolean; defaultReasoningEffort: string; reasoningEfforts: string[]; recommendedUseCases: string[]}
export interface RouteView extends ProductProfile {discovered: boolean; enabled: boolean; availability: AvailabilityStatus; routable: boolean; diagnostic?: string}
export interface DriverListResponse {schemaVersion: typeof configApiVersion; drivers: Array<{driver: string; provider: string; workflowEligible: boolean; modelCount: number; lastDiscoveredAt?: string}>}
export interface DiscoveryResponse {schemaVersion: typeof configApiVersion; driver: string; observedAt: string; models: ModelInfo[]; routes: RouteView[]}
export interface AvailabilityEntry {routeId: string; model: string; status: AvailabilityStatus; reasoningEffort?: string; observedAt?: string; expiresAt?: string; diagnostic?: string}
export interface ProbeResponse {schemaVersion: typeof configApiVersion; entries: AvailabilityEntry[]; catalogHash: string}
export interface RoutingConfig {schemaVersion: typeof configApiVersion; revision: number; routes: Array<{routeId: string; enabled: boolean}>; updatedAt: string}
export interface ConfigGetResponse {schemaVersion: typeof configApiVersion; config: RoutingConfig}
export interface ConfigUpdateResponse {schemaVersion: typeof configApiVersion; config: RoutingConfig; replayed: boolean}
export interface AvailabilityResponse {schemaVersion: typeof configApiVersion; entries: AvailabilityEntry[]}
export interface EffectiveCatalogResponse {schemaVersion: typeof configApiVersion; source: string; catalogHash: string; catalog: RoutingCatalog; routes: RouteView[]}

export interface RoutingResponses {
  'driver.list': DriverListResponse;
  'driver.models.discover': DiscoveryResponse;
  'driver.models.probe': ProbeResponse;
  'routing.config.get': ConfigGetResponse;
  'routing.config.update': ConfigUpdateResponse;
  'routing.availability': AvailabilityResponse;
  'routing.catalog.effective': EffectiveCatalogResponse;
}

export interface RoutingError {code: string; message: string}
export class RoutingCallError extends Error {constructor(readonly routingError: RoutingError) {super(routingError.message); this.name = 'RoutingCallError'}}

export async function callRouting<M extends RoutingMethod>(client: EngineClient, method: M, request: unknown): Promise<RoutingResponses[M]> {
  try {return await client.call<RoutingResponses[M]>(method, request)}
  catch (error) {
    if (error instanceof EngineRpcError && error.data && typeof error.data === 'object' && typeof (error.data as {code?: unknown}).code === 'string') {
      throw new RoutingCallError({code: (error.data as {code: string}).code, message: error.message});
    }
    throw error;
  }
}

