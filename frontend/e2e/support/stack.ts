// Ports and fixture identities for the throwaway E2E stack.
// The live Docker API on port 8080 is never used by these tests.

export const API_PORT = 18080;
export const API_URL = `http://localhost:${API_PORT}`;
export const WEB_PORT = 4300;

export const PARENT = {
  userId: 'e2e-parent',
  password: 'e2e-parent-pass',
  displayName: 'E2E Parent',
  email: 'e2e-parent@example.test',
  familyName: 'E2E Family',
};

export const CHILD = {
  userId: 'e2e-child',
  password: 'e2e-child-pass',
  displayName: 'E2E Child',
  colour: '#4F8A6B',
};

export const TEMPLATE = {
  name: 'E2E Tidy room',
  title: 'E2E Tidy room',
  points: 5,
};
