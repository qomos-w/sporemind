import { SchemaRegistry } from '@qomos/spore-ts'
import type { ObjectDesc, TypeDesc } from '@qomos/spore-ts/schema'
import type { SchemaEntry } from '@qomos/spore-ts/registry'
import type { AppObjectDescriptor, AppTypeDescriptor } from '../gen-types/app'

const descriptorsByApp = new Map<string, { namespace: string; descriptors: Record<string, AppObjectDescriptor> }>()
let registry = new SchemaRegistry()
let entriesByName = new Map<string, SchemaEntry[]>()

function typeDescriptor(value: AppTypeDescriptor): TypeDesc {
  return {
    kind: value.Kind as TypeDesc['kind'],
    name: value.Name,
    typeId: value.TypeId,
    element: value.Element ? typeDescriptor(value.Element) : undefined,
    key: value.Key ? typeDescriptor(value.Key) : undefined,
    value: value.Value ? typeDescriptor(value.Value) : undefined,
    className: value.ClassName,
    classId: value.ClassId,
  }
}

function objectDescriptor(value: AppObjectDescriptor): ObjectDesc {
  return {
    kind: value.Kind as ObjectDesc['kind'],
    name: value.Name,
    fields: value.Fields.map(field => ({
      name: field.Name,
      type: typeDescriptor(field.Type),
      description: field.Description,
      optional: field.Optional,
    })),
  }
}

function rebuild(): void {
  const nextRegistry = new SchemaRegistry()
  const nextEntries = new Map<string, SchemaEntry[]>()
  for (const { namespace, descriptors } of descriptorsByApp.values()) {
    for (const descriptor of Object.values(descriptors)) {
      const object = objectDescriptor(descriptor)
      const entry: SchemaEntry = {
        namespace,
        schemaId: descriptor.SchemaId!,
        name: descriptor.Name,
        type: { kind: descriptor.Kind as TypeDesc['kind'], name: descriptor.Name },
        object,
      }
      nextRegistry.register(entry)
      nextEntries.set(entry.name, [...(nextEntries.get(entry.name) ?? []), entry])
    }
  }
  registry = nextRegistry
  entriesByName = nextEntries
}

export function replaceAppSchemas(appId: string, namespace: string, descriptors?: Record<string, AppObjectDescriptor>): void {
  if (!namespace || !descriptors || Object.keys(descriptors).length === 0) descriptorsByApp.delete(appId)
  else descriptorsByApp.set(appId, { namespace, descriptors })
  rebuild()
}

export function removeAppSchemas(appId: string): void {
  if (descriptorsByApp.delete(appId)) rebuild()
}

export function replaceAllAppSchemas(apps: ReadonlyArray<{ id: string; namespace?: string; schemaDescriptors?: Record<string, AppObjectDescriptor> }>): void {
  descriptorsByApp.clear()
  for (const app of apps) {
    if (app.namespace && app.schemaDescriptors && Object.keys(app.schemaDescriptors).length > 0) {
      descriptorsByApp.set(app.id, { namespace: app.namespace, descriptors: app.schemaDescriptors })
    }
  }
  rebuild()
}

export function resolveDynamicSchema(schemaName: string, namespace?: string): { type: TypeDesc; objectFor: (name: string) => ObjectDesc | undefined } | undefined {
  const entries = entriesByName.get(schemaName) ?? []
  const entry = namespace ? entries.find(candidate => candidate.namespace === namespace) : entries.length === 1 ? entries[0] : undefined
  if (!entry) return undefined
  return { type: entry.type, objectFor: registry.objectFor(entry.namespace) }
}
