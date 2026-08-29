/** Shared wire description for the small Fishyume DSH Remote. */

interface JsonSchema<T> { parse(value: unknown): T }

function jsonValue(value: unknown, seen = new Set<object>()): unknown {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return value
  if (typeof value === 'number') {
    if (Number.isFinite(value)) return value
    throw new TypeError('value must contain only finite numbers')
  }
  if (typeof value !== 'object') throw new TypeError('value must be JSON-safe')
  if (seen.has(value)) throw new TypeError('value must not be cyclic')
  seen.add(value)
  try {
    if (Array.isArray(value)) return value.map((item) => jsonValue(item, seen))
    const output: Record<string, unknown> = {}
    for (const [key, item] of Object.entries(value)) output[key] = jsonValue(item, seen)
    return output
  } finally { seen.delete(value) }
}

const methodSchema: JsonSchema<string> = {
  parse(value) {
    if (typeof value !== 'string' || value.length === 0) throw new TypeError('method must be a non-empty string')
    return value
  },
}

const jsonSchema: JsonSchema<unknown> = { parse: (value) => jsonValue(value) }

export const FISHYUME_INVOCATIONS = [
  {
    id: 'dsh-fishyume#fishyume/call',
    service: 'fishyume',
    namespace: 'fishyume',
    method: 'call',
    invocation: { kind: 'direct' },
    parameters: [
      { name: 'method', wire: 'method', source: 'json', codec: { mode: 'strict', typeSymbol: 'dsh-fishyume#Method', schema: methodSchema } },
      { name: 'params', wire: 'params', source: 'json', acceptsUndefined: true, codec: { mode: 'strict', typeSymbol: 'dsh-fishyume#Params', schema: jsonSchema } },
    ],
    result: { mode: 'strict', typeSymbol: 'dsh-fishyume#Result', schema: jsonSchema },
  },
] as const

export const FISHYUME_REMOTE = {
  package: 'dsh-fishyume',
  descriptors: FISHYUME_INVOCATIONS,
} as const

export const FISHYUME_TYPERT_MANIFEST = {
  package: 'dsh-fishyume',
  face: 'host',
  schemas: [],
  model: {
    services: [{
      key: 'fishyume',
      exportName: 'FishyumeRemote',
      description: 'Fishyume Team, Workflow, Run, and Routing control-plane calls.',
      tags: [],
      members: [{
        kind: 'method',
        name: 'call',
        signature: 'call(method: string, params?: unknown): Promise<unknown>',
      }],
      types: [],
    }],
    events: [],
    objects: [],
  },
  invocations: FISHYUME_INVOCATIONS,
} as const

export interface FishyumeRemoteFace {
  call(method: string, params?: unknown): Promise<{ ok: true; value: unknown } | { ok: false; error: { code: string; message: string; details?: unknown } }>
}
