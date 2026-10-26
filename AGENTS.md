# NestWall agent guide

This repository builds NestWall, a self-hosted family planner (Angular, Go, SQLite with SQLCipher, Docker).

Before starting work:

1. Read `design/FINAL-PROGRESS.md` for open fixes, improvements and extra features. Tick items there when they are done, with the date.
2. For releases, follow `docs/release-guide.md`. Versions come from the commit messages (see `scripts/bump-version.sh`).
3. For install and operations, see `docs/install-guide.md` (end users) and `docs/dev-guide.md` (building from source), plus `docs/backup-restore.md`.

Full specification: kept locally, not in the repository. Brand assets: `frontend/src/assets/images/` and `frontend/src/assets/fonts/` (Nesty logo, Fredoka font, icons). Regenerate the brand images with `python design/tools/brand.py` while the local `design/` folder exists.
