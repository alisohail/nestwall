# Contributing to NestWall

Thanks for helping make NestWall better. Bug reports, feature ideas and pull requests are all welcome.

## Before you start

- For a bug, open an issue with the bug report template so it can be reproduced.
- For a larger change, open an issue first so we can agree on the approach.
- Security problems: see [SECURITY.md](SECURITY.md). Do not open a public issue.

## Development setup

See [docs/dev-guide.md](docs/dev-guide.md) for building from source and running the stack locally.

Quick checks:

```bash
# Backend
cd backend
go vet ./...
go test ./...

# Frontend
cd frontend
npm ci
npm run build
npx ng lint
npx stylelint "src/**/*.scss"
npx ng test --watch=false --browsers=ChromeHeadless
npm run e2e   # needs Go; starts a throwaway API on port 18080
```

## Pull requests

1. Fork the repository and create a branch from `main`.
2. Keep each PR focused on one change.
3. Make sure the CI checks pass. Every PR runs backend tests, the frontend build, ESLint, stylelint, unit tests and the end-to-end tests.
4. Describe what changed and why, and how you tested it. Use the PR template.
5. Update the docs when behaviour or setup changes.

## Commit messages

Releases are versioned from commit messages, so please use [Conventional Commits](https://www.conventionalcommits.org/):

- `fix: ...` for a bug fix (patch release)
- `feat: ...` for a new feature (minor release)
- `feat!: ...` or a `BREAKING CHANGE:` footer for a breaking change (major release)
- `docs:`, `chore:`, `ci:`, `test:`, `refactor:` for everything else

## Code of conduct

By taking part you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
