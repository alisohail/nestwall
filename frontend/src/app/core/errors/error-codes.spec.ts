import { errorMessageKey, errorStatus } from './error-codes';

describe('errorMessageKey', () => {
  it('maps API error codes', () => {
    expect(errorMessageKey({ code: 'forbidden', status: 403 })).toBe('errors.messages.forbidden');
    expect(errorMessageKey({ code: 'permission_denied' })).toBe('errors.messages.forbidden');
    expect(errorMessageKey({ code: 'not_found', status: 404 })).toBe('errors.messages.not_found');
    expect(errorMessageKey({ code: 'version_conflict', status: 412 })).toBe('errors.messages.conflict');
    expect(errorMessageKey({ code: 'internal_error', status: 500 })).toBe('errors.messages.generic');
  });

  it('falls back to the HTTP status when the code is unknown', () => {
    expect(errorMessageKey({ code: 'something_new', status: 401 })).toBe('errors.messages.session');
    expect(errorMessageKey({ code: 'something_new', status: 409 })).toBe('errors.messages.conflict');
    expect(errorMessageKey({ status: 0 })).toBe('errors.messages.network');
  });

  it('returns the generic message for anything else', () => {
    expect(errorMessageKey(new Error('boom'))).toBe('errors.messages.generic');
    expect(errorMessageKey(null)).toBe('errors.messages.generic');
    expect(errorMessageKey('text')).toBe('errors.messages.generic');
  });
});

describe('errorStatus', () => {
  it('reads a numeric status and ignores other values', () => {
    expect(errorStatus({ status: 503 })).toBe(503);
    expect(errorStatus({ status: '503' })).toBeUndefined();
    expect(errorStatus(new Error('x'))).toBeUndefined();
  });
});
