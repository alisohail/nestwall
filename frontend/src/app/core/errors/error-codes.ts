/**
 * Maps an API error (or any thrown value) to an i18n key under `errors.messages`.
 * Matches on the API error code first, then on the HTTP status, and falls back to a generic message.
 */
export function errorMessageKey(error: unknown): string {
  const { code, status } = readFields(error);

  switch (code) {
    case 'forbidden':
    case 'permission_denied':
      return 'errors.messages.forbidden';
    case 'not_found':
      return 'errors.messages.not_found';
    case 'conflict':
    case 'version_conflict':
      return 'errors.messages.conflict';
    case 'internal_error':
      return 'errors.messages.generic';
  }

  switch (status) {
    case 0:
      return 'errors.messages.network';
    case 401:
      return 'errors.messages.session';
    case 403:
      return 'errors.messages.forbidden';
    case 404:
      return 'errors.messages.not_found';
    case 409:
    case 412:
      return 'errors.messages.conflict';
    default:
      return 'errors.messages.generic';
  }
}

/** Returns the HTTP status carried by an API error, or `undefined` for non-HTTP errors. */
export function errorStatus(error: unknown): number | undefined {
  return readFields(error).status;
}

function readFields(error: unknown): { code?: string; status?: number } {
  if (!error || typeof error !== 'object') {
    return {};
  }
  const candidate = error as { code?: unknown; status?: unknown };
  return {
    code: typeof candidate.code === 'string' ? candidate.code : undefined,
    status: typeof candidate.status === 'number' ? candidate.status : undefined,
  };
}
