# Release guide

This guide is for maintainers. It covers how a new NestWall version is numbered, published to GitHub Container Registry, and rolled back. Users who want to install or upgrade should read [install-guide.md](./install-guide.md). Developers who build from source should read [dev-guide.md](./dev-guide.md).

## How versions work

- The version lives in [VERSION](../VERSION) as `MAJOR.MINOR.PATCH`, for example `0.1.0`, and is copied to [frontend/package.json](../frontend/package.json) and [frontend/package-lock.json](../frontend/package-lock.json) on each release. Do not edit it by hand for normal releases.
- Versions follow [semantic versioning](https://semver.org/). The bump is computed from the commit messages since the last `v*` tag by [scripts/bump-version.sh](../scripts/bump-version.sh):

| Commit message | Bump | Example |
|---|---|---|
| `feat(...): ...` or `feat: ...` | minor | `0.1.0` to `0.2.0` |
| `type!: ...` or a `BREAKING CHANGE:` footer | major | `0.2.0` to `1.0.0` |
| anything else (`fix`, `docs`, `ci`, `chore`, ...) | patch | `0.2.0` to `0.2.1` |

If several commits are pending, the highest bump wins.

## Automatic release on push to main

Pushing to `main` runs [release.yml](../.github/workflows/release.yml):

1. It computes the new version from the commits since the last tag.
2. It commits `VERSION`, `frontend/package.json` and `frontend/package-lock.json` back to `main` as `chore(release): vX.Y.Z [skip ci]` and creates the tag `vX.Y.Z`.
3. It calls [docker-publish.yml](../.github/workflows/docker-publish.yml), which builds and pushes the images and creates the GitHub Release with generated notes.

Notes:

- A push that only changes `VERSION` or Markdown files does not release.
- Commits with `[skip ci]` in the message do not release.
- Pushes to `main` run one at a time, so two merges cannot race.

## Manual release

Use this when you want to publish a version that already has a tag, or to retry a failed publish.

- **Republish an existing tag:** in GitHub, open **Actions > Publish Docker images > Run workflow** and enter the version without the `v`, for example `0.2.0`. The tag `v0.2.0` must exist.
- **Publish by pushing a tag:** `git tag v0.2.0 && git push origin v0.2.0`. This runs the publish workflow for that tag.
- **Run the bump again:** in GitHub, open **Actions > Release > Run workflow**. This computes a new version from the commits since the last tag and releases it. If there are no new commits since the last tag, the run skips the release and the publish.

## What gets published

Each release publishes two images for `linux/amd64`:

- `ghcr.io/alisohail/nestwall-api`
- `ghcr.io/alisohail/nestwall-web`

Each image has the tags `X.Y.Z`, `X.Y` and `latest`. The GitHub Release for the tag lists the changes.

## Before you release

1. Make sure `main` is green: run the backend tests, the frontend tests and the Docker build.
2. Check the commit messages since the last tag. The version number comes from them, so use `feat:`, `fix:` and `!` deliberately.
3. Update [install-guide.md](./install-guide.md) and [dev-guide.md](./dev-guide.md) if an install step, environment variable or upgrade step changed.

## After you release

1. Check that the **Release** and **Publish Docker images** runs both succeeded in the Actions tab.
2. Check the images are on the GitHub Packages page, with the expected tags.
3. Pull and start the new version on a test host:

   ```bash
   docker pull ghcr.io/alisohail/nestwall-api:X.Y.Z
   docker pull ghcr.io/alisohail/nestwall-web:X.Y.Z
   ```

4. Follow the upgrade steps in [install-guide.md](./install-guide.md).

## Rolling back

Published images are not deleted, so a bad release can be rolled back by pinning the previous version.

- **For users:** set the image tags in the compose file to the last good version, then run `docker compose pull` and `docker compose up -d`. The data in `database/` is kept. If a migration changed the database, restore the backup taken before the upgrade (see [backup-restore.md](./backup-restore.md)).
- **For the repository:** fix forward with a new commit to `main`. That makes a new patch release. Do not rewrite `main` or move existing tags, because users may already have pulled them.

## Troubleshooting

- **The release did not run:** the change may have touched only `VERSION` or Markdown files, or the commit message may contain `[skip ci]`.
- **The bump push was rejected:** `main` has a ruleset that requires the CI checks and blocks direct pushes. Check that the `RELEASE_TOKEN` secret is set (a PAT from an admin account, with `contents: write`). The release job uses it to push past the ruleset. Without it, the push falls back to the default token and is rejected.
- **The publish step failed:** check that the Actions settings allow read and write for `GITHUB_TOKEN` (for packages and contents). Then republish with the manual run above.
- **The version is wrong:** the bump is computed only from commit messages since the last tag, and any commit without `feat` or `!` gives a patch bump. Fix the commit history before the release runs. If a wrong version was already published, release a corrected version with a new commit. Do not reuse or move the old tag.
