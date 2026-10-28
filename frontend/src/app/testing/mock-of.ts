import type { MockedObject } from 'vitest';

/** Builds a typed partial test double; only the members a spec uses need to be supplied. */
export function mockOf<T>(partial: Partial<Record<keyof T, unknown>>): MockedObject<T> {
  return partial as MockedObject<T>;
}
