import { request, type APIRequestContext } from '@playwright/test';
import { API_URL, CHILD, PARENT, TEMPLATE } from './stack';

/**
 * Creates the parent, child and chore template through the API on a fresh stack.
 * The journey itself runs through the UI; only these fixtures are seeded here.
 *
 * Every spec calls this in its beforeAll. The first call seeds the family; later calls in the
 * same run find setup complete and do nothing, so specs share one family on one stack.
 */
export async function seedFreshFamily(): Promise<void> {
  const api = await request.newContext({ baseURL: API_URL });
  try {
    // The first request issues the CSRF cookie that state-changing calls echo back.
    const status = await api.get('/api/v1/setup/status');
    if (!status.ok()) {
      throw new Error(`setup status failed: ${status.status()}`);
    }
    const body = (await status.json()) as { setup_required: boolean };
    if (!body.setup_required) {
      return;
    }

    await post(api, '/api/v1/setup/parent', {
      user_id: PARENT.userId,
      password: PARENT.password,
      display_name: PARENT.displayName,
      email: PARENT.email,
      family_name: PARENT.familyName,
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      locale: 'en',
    });

    // Setup does not issue a session cookie, so sign in as the parent for the rest of the seed.
    await post(api, '/api/v1/auth/login', { user_id: PARENT.userId, password: PARENT.password });

    const roles = (await (await api.get('/api/v1/family/roles')).json()) as {
      roles: { id: string; name: string }[];
    };
    const childRole = roles.roles.find((role) => role.name === 'Child');
    if (!childRole) {
      throw new Error('built-in Child role not found');
    }

    await post(api, '/api/v1/family/members', {
      role_id: childRole.id,
      user_id: CHILD.userId,
      password: CHILD.password,
      display_name: CHILD.displayName,
      colour: CHILD.colour,
    });

    await post(api, '/api/v1/task-templates', {
      name: TEMPLATE.name,
      title: TEMPLATE.title,
      points: TEMPLATE.points,
      approval_required: true,
    });
  } finally {
    await api.dispose();
  }
}

async function post(api: APIRequestContext, path: string, data: unknown): Promise<void> {
  const state = await api.storageState();
  const csrf = state.cookies.find((c) => c.name === 'nestwall-csrf')?.value;
  if (!csrf) {
    throw new Error(`no CSRF cookie before POST ${path}`);
  }
  const res = await api.post(path, { data, headers: { 'X-CSRF-Token': csrf } });
  if (!res.ok()) {
    throw new Error(`POST ${path} failed: ${res.status()} ${await res.text()}`);
  }
}
