import { ApiError } from '../../core/api/api-client.service';
import { TaskCategory, TaskSettingsResponse, TaskTemplate } from '../tasks/tasks.models';
import {
  approvalChoice,
  approvalValue,
  categoryBody,
  categoryToForm,
  emptyTemplateForm,
  expiryMinutes,
  expiryParts,
  isExpiryValid,
  prefillCount,
  settingsBody,
  settingsErrorKey,
  templateBody,
  templateToForm,
  timesInOrder,
} from './settings.format';

describe('settings format helpers', () => {
  describe('expiry', () => {
    it('shows whole days in days', () => {
      expect(expiryParts(2880)).toEqual({ value: 2, unit: 'days' });
    });

    it('shows other durations in hours, including fractions', () => {
      expect(expiryParts(90)).toEqual({ value: 1.5, unit: 'hours' });
      expect(expiryParts(180)).toEqual({ value: 3, unit: 'hours' });
    });

    it('returns null for no default', () => {
      expect(expiryParts(null)).toEqual({ value: null, unit: 'days' });
      expect(expiryMinutes(null, 'days')).toBeNull();
    });

    it('round-trips through minutes', () => {
      for (const minutes of [1440, 2880, 60, 90, 180]) {
        const parts = expiryParts(minutes);
        expect(expiryMinutes(parts.value, parts.unit)).toBe(minutes);
      }
    });

    it('requires whole days and a positive value', () => {
      expect(isExpiryValid(null, 'days')).toBeTrue();
      expect(isExpiryValid(2, 'days')).toBeTrue();
      expect(isExpiryValid(1.5, 'days')).toBeFalse();
      expect(isExpiryValid(1.5, 'hours')).toBeTrue();
      expect(isExpiryValid(0, 'hours')).toBeFalse();
    });
  });

  describe('timesInOrder', () => {
    it('accepts strictly increasing times', () => {
      expect(timesInOrder({ morning_start: '05:00', after_school_start: '12:00', evening_start: '17:00' })).toBeTrue();
    });

    it('rejects equal, decreasing or empty times', () => {
      expect(timesInOrder({ morning_start: '12:00', after_school_start: '12:00', evening_start: '17:00' })).toBeFalse();
      expect(timesInOrder({ morning_start: '13:00', after_school_start: '12:00', evening_start: '17:00' })).toBeFalse();
      expect(timesInOrder({ morning_start: '05:00', after_school_start: '', evening_start: '17:00' })).toBeFalse();
    });
  });

  describe('templates', () => {
    const template: TaskTemplate = {
      id: 't1',
      kind: 'regular',
      name: 'Feed the cat',
      category_id: 'c1',
      title: 'Feed Mochi',
      description: null,
      part_of_day: 'morning',
      priority: 'normal',
      due_time: '07:30',
      reminder_minutes_before: null,
      late_points_percent: 50,
      default_assignee_ids: ['m1'],
      points: 3,
      approval_required: true,
      expires_after_minutes: 2880,
      penalty_value: 10,
      penalty_unit: 'percent',
      version: 4,
    };

    it('builds an empty body with no prefilled fields', () => {
      const body = templateBody({ ...emptyTemplateForm(), name: '  Pets  ' });
      expect(body.name).toBe('Pets');
      expect(prefillCount(body)).toBe(0);
      expect(body.penalty_unit).toBeNull();
    });

    it('omits the hidden fields from the body so PATCH keeps them', () => {
      const body = templateBody(templateToForm(template));
      expect('part_of_day' in body).toBeFalse();
      expect('priority' in body).toBeFalse();
      expect('approval_required' in body).toBeFalse();
    });

    it('counts set fields and round-trips the stored values', () => {
      const body = templateBody(templateToForm(template));
      expect(body.expires_after_minutes).toBe(2880);
      expect(body.penalty_value).toBe(10);
      expect(body.penalty_unit).toBe('percent');
      // category, title, points, due time, penalty, expiry, late points, assignees
      expect(prefillCount(body)).toBe(8);
    });

    it('clears the penalty unit when the penalty value is empty', () => {
      const body = templateBody({ ...templateToForm(template), penalty_value: null });
      expect(body.penalty_value).toBeNull();
      expect(body.penalty_unit).toBeNull();
    });
  });

  describe('categories', () => {
    const existing: TaskCategory = {
      id: 'c1',
      seed_key: 'chores',
      name: 'Chores',
      emoji: '🧹',
      colour: '#0F766E',
      sort_order: 2,
      badge_key: 'chores_star',
      points: 5,
      approval_required: null,
      expires_after_minutes: null,
      penalty_value: 3,
      penalty_unit: 'points',
      version: 7,
    };

    it('keeps the fields the dialog does not show on PUT', () => {
      const body = categoryBody(
        { name: ' Chores ', emoji: '🧹', colour: '#0F766E', points: 6, approval_required: true, expiry_value: 1, expiry_unit: 'days', badge_key: 'tidy_pro' },
        existing,
      );
      expect(body.name).toBe('Chores');
      expect(body.sort_order).toBe(2);
      expect(body.badge_key).toBe('tidy_pro');
      expect(body.penalty_value).toBe(3);
      expect(body.penalty_unit).toBe('points');
      expect(body.expires_after_minutes).toBe(1440);
    });

    it('sends the skill badge the form picked, including none', () => {
      const base = { name: 'Chores', emoji: '🧹', colour: '#0F766E', points: null, approval_required: null, expiry_value: null, expiry_unit: 'days' as const };
      expect(categoryBody({ ...base, badge_key: null }, existing).badge_key).toBeNull();
      expect(categoryBody({ ...base, badge_key: 'pet_pal' }, existing).badge_key).toBe('pet_pal');
    });

    it('reads the skill badge into the form', () => {
      expect(categoryToForm(existing).badge_key).toBe('chores_star');
    });

    it('sends no sort order or penalty on create', () => {
      const body = categoryBody(
        { name: 'Pets', emoji: '🐶', colour: '#F59E0B', points: null, approval_required: null, expiry_value: null, expiry_unit: 'days', badge_key: null },
        null,
      );
      expect(body.sort_order).toBeUndefined();
      expect(body.penalty_value).toBeNull();
    });
  });

  describe('settings body', () => {
    it('resends the penalty fields and the other part-of-day times', () => {
      const current: TaskSettingsResponse = {
        defaults: { points: 5, approval_required: null, missed_after: null, penalty_value: 20, penalty_unit: 'percent' },
        part_of_day: { morning_start: '05:00', after_school_start: '12:00', evening_start: '17:00' },
        bonus_defaults: { points: 15, claim_mode: 'first_come', show_from: '08:00', ends: 'end_of_day', approval_required: true },
        version: 3,
      };
      const body = settingsBody(current, { defaults: { points: 8 } });
      expect(body.defaults.points).toBe(8);
      expect(body.defaults.penalty_value).toBe(20);
      expect(body.part_of_day).toEqual(current.part_of_day);
    });

    it('keeps the bonus defaults unless they are changed', () => {
      const current: TaskSettingsResponse = {
        defaults: { points: null, approval_required: null, missed_after: null, penalty_value: null, penalty_unit: null },
        part_of_day: { morning_start: '05:00', after_school_start: '12:00', evening_start: '17:00' },
        bonus_defaults: { points: 15, claim_mode: 'everyone_once', show_from: null, ends: null, approval_required: false },
        version: 3,
      };
      expect(settingsBody(current, { defaults: { points: 8 } }).bonus_defaults).toEqual(current.bonus_defaults);

      const next = { points: 20, claim_mode: null, show_from: '07:30', ends: 'end_of_day', approval_required: true } as const;
      expect(settingsBody(current, { bonusDefaults: { ...next } }).bonus_defaults).toEqual(next);
    });
  });

  describe('approval choice', () => {
    it('round-trips null, required and not required', () => {
      for (const value of [null, true, false]) {
        expect(approvalValue(approvalChoice(value))).toBe(value);
      }
    });
  });

  describe('settingsErrorKey', () => {
    const apiError = (status: number, code: string, params?: Record<string, string>): ApiError => ({
      status,
      code,
      message: '',
      params,
    });

    it('maps the in-use and name conflicts', () => {
      expect(settingsErrorKey(apiError(409, 'template_in_use'))).toBe('settings.error.template_in_use');
      expect(settingsErrorKey(apiError(409, 'category_in_use'))).toBe('settings.error.category_in_use');
      expect(settingsErrorKey(apiError(409, 'name_taken'))).toBe('settings.error.name_taken');
    });

    it('maps out-of-order times and the generic statuses', () => {
      expect(settingsErrorKey(apiError(400, 'validation_failed', { field: 'part_of_day', reason: 'out_of_order' }))).toBe(
        'settings.error.out_of_order',
      );
      expect(settingsErrorKey(apiError(412, 'version_conflict'))).toBe('settings.error.stale');
      expect(settingsErrorKey(apiError(0, ''))).toBe('settings.error.network');
      expect(settingsErrorKey(apiError(500, 'internal_error'))).toBe('settings.error.generic');
    });
  });
});
