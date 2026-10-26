/** Returns a random 32-character hex key. Works in insecure (plain HTTP) contexts, unlike randomUUID. */
export function newIdempotencyKey(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => b.toString(16).padStart(2, '0')).join('');
}
