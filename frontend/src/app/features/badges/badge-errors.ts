import { ApiError } from '../../core/api/api-client.service';
import { settingsErrorKey } from '../settings/settings.format';

/** Badge errors first, then the shared settings mapping. */
export function badgeErrorKey(err: ApiError): string {
  switch (err.code) {
    case 'badge_disabled':
      return 'settings.badges.error.badge_disabled';
    case 'badges_off':
      return 'settings.badges.error.badges_off';
    case 'idempotency_conflict':
      return 'settings.badges.error.idempotency_conflict';
  }
  return settingsErrorKey(err);
}
