# NestWall developer guide: build from source, upgrade and operations

This guide takes a new host from nothing to a running family planner. It covers install, first sign-in, wall displays, upgrade, rollback, NAS notes and troubleshooting. Backups and restores are in [backup-restore.md](./backup-restore.md). Home Assistant is in [home-assistant.md](./home-assistant.md).

## What you need

- Docker Engine with the Compose plugin (`docker compose version` should print a version).
- Git, to get the code.
- About 1 GB of free disk space for the images, plus room for the database and backups.
- A host that stays on. The family reaches NestWall over the local network.

NestWall runs as two containers:

| Container | Role | Host port |
|---|---|---|
| `web` | Nginx serving the Angular app | `WEB_PORT` (default `5842`) |
| `api` | Go API with the encrypted SQLite database | not published (reached through `web`) |

## 1. Get the code

```bash
git clone <repository-url> nestwall
cd nestwall
```

## 2. Create the settings file

```bash
cp .env.example deploy/.env
```

Edit `deploy/.env` and set these values. The API will not start without the first two.

| Variable | What to set |
|---|---|
| `DB_KEY` | A long random passphrase that encrypts the database. Without it the data cannot be read. Keep a copy outside the repo. |
| `SESSION_SECRET` | A long random string used to sign sessions. Generate one with `openssl rand -base64 48`. |
| `APP_URL` | The address people use, for example `http://192.168.1.20:5842`. Password reset links are built from it. |
| `TZ` | The family's time zone, for example `Europe/London`. Scheduled backups run at the saved hour in this zone. |

Do not commit `deploy/.env`. It is git-ignored.

## 3. Start the stack

```bash
docker compose -f deploy/compose.yaml up -d --build
docker compose -f deploy/compose.yaml ps
```

The first build takes a few minutes. When both containers are running, `api` shows `healthy` and `web` shows `Up`. The first start creates `database/nestwall.db` in the repo root.

Check the install:

- Open `http://localhost:5842` (or the host's address) in a browser. The sign-in page loads.
- `docker compose -f deploy/compose.yaml exec api /app/api healthcheck` exits with code 0.

## 4. Create the first parent

On a new install the first page is the setup screen. It offers two choices:

- **Create the family**: set up the first parent account and the family members.
- **Restore from a backup**: upload an archive from an earlier install. Enter its passphrase if it is encrypted. See [backup-restore.md](./backup-restore.md#first-run-restore-new-install).

If the first parent has to change a password, the app asks for a new one before anything else. Until it is changed, API calls return `403 password_change_required`.

## 5. Set up a wall display

A wall display is a tablet or screen that shows the family wall. Each one is a paired device with its own exit code.

1. Sign in as a parent and open **Settings > Wall devices**.
2. Select **Add wall device** and give it a name, for example `Kitchen tablet`.
3. On the tablet, open `http://<host>:5842/pair` (or select **Set up a wall display** on the sign-in page).
4. Enter the pairing key shown in Settings. Keys expire after 10 minutes and work once.
5. The tablet shows the wall. Set the exit code shown on the device card if you want to leave the wall from the tablet.

Revoke a device in **Settings > Wall devices** to stop it at once. Five wrong exit codes lock the device out for 5 minutes.

## 6. Keep it running

| Task | Command or place |
|---|---|
| See logs | `docker compose -f deploy/compose.yaml logs -f api` |
| Restart | `docker compose -f deploy/compose.yaml restart` |
| Stop | `docker compose -f deploy/compose.yaml down` (keeps `database/`) |
| Back up now | **Settings > Data > Backups > Back up now**. Archives go to `database/backups/`. |
| Diagnostics | **Settings > Diagnostics**: version, uptime, backup and sync health, email queue, wall devices, recent server errors. |

The containers restart on their own after a reboot (`restart: unless-stopped`).

## 7. Upgrade

Make a backup first. An upgrade runs database migrations, and a backup is the only way back.

```bash
# 1. Back up in the app: Settings > Data > Backups > Back up now (keep the archive off the host).
# 2. Get the new code
git pull
# 3. Rebuild and restart
docker compose -f deploy/compose.yaml up -d --build
# 4. Check
docker compose -f deploy/compose.yaml ps
```

Confirm the new version in **Settings > Diagnostics** (the version line shows the build's `VERSION`).

## 8. Roll back

Migrations only move forward, and an archive from a newer version is refused by an older API. So a rollback is: the previous code, plus the backup taken before the upgrade.

```bash
docker compose -f deploy/compose.yaml down
git checkout <previous-tag-or-commit>
docker compose -f deploy/compose.yaml up -d --build
```

If the upgrade changed the data, restore the pre-upgrade archive with the old version using the command line. Stop the API first:

```bash
docker compose -f deploy/compose.yaml run --rm api restore /app/database/backups/<archive>.zip
docker compose -f deploy/compose.yaml up -d
```

The `run` service already gets `DB_PATH`, `DB_KEY` and `BACKUP_DIR` from `deploy/compose.yaml`. For an encrypted archive, also pass the passphrase with `-e NESTWALL_RESTORE_PASSPHRASE` (see [backup-restore.md](./backup-restore.md#restore-from-the-command-line)). Keep the database and the backup until the rollback is confirmed.

## 9. NAS notes

- **Use the NAS's Docker or Container Manager.** Create a project from `deploy/compose.yaml`, with the repo folder as the project folder, and set the environment from `deploy/.env`.
- **Keep the data on a volume you back up.** The database and archives live in `database/` in the repo folder (`database/nestwall.db` and `database/backups/`). Back up that folder too, or use the off-NAS copies planned for a later release.
- **Make `database/` writable by the container.** The API writes there as a non-root user. If the API logs a permission error on the database, fix the owner of `database/` on the host.
- **Architecture.** The published images are built for amd64 only, so they run on Intel and AMD hosts, including most NAS boxes. On an ARM host (Raspberry Pi, Apple Silicon, ARM NAS), the published images won't run. Build the stack on the host instead with `docker compose up -d --build` from the `deploy/` folder. The compose file builds from source. Check the image architecture with `docker image inspect` if a platform mismatch is suspected.
- **Time zone.** Set `TZ` so scheduled backups run at the hour the family expects.
- **Ports.** The browser port is `WEB_PORT` in `deploy/.env` (default `5842`). Set `APP_URL` to the same port. Leave `API_PORT` at `8080`: the web container proxies to `api:8080` inside the Docker network, so changing it breaks the app.

## 10. Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `api` restarts or never becomes `healthy` | Check `docker compose ... logs api`. Most often `DB_KEY` or `SESSION_SECRET` is missing from `deploy/.env`. |
| `failed to open database` | `DB_KEY` does not match the key the database was created with. Restore the matching key. Do not delete `database/` to get past it. |
| `permission denied` on the database or backups | Fix the owner of `database/` on the host (see NAS notes). |
| Page loads but sign-in fails with a 5xx | Check the API logs and **Settings > Diagnostics > Server errors**. |
| Password reset link points to the wrong address | Set `APP_URL` to the address people use, then restart. |
| Wall display says "Waiting for pairing" | The pairing key expired (10 minutes) or was already used. Make a new key on the device card. |
| Wall display offline | Check the tablet's network and that it can reach `http://<host>:5842`. |
| Backups missing | **Settings > Data > Backups** shows the history. A failed scheduled run notifies parents. |
| `403 password_change_required` on every call | The parent must change the password first. |
| Restore refused, passphrase wrong | Check `NESTWALL_RESTORE_PASSPHRASE` has no extra spaces or quotes. |
| Restore refused, newer version | The archive is from a newer NestWall. Upgrade the API first. |
| Port already in use | Set `WEB_PORT` in `deploy/.env` to a free port (for example `8090`), set `APP_URL` to match, and run the start command again. |

## Reference

- Environment variables: [.env.example](../.env.example)
- Compose file: [deploy/compose.yaml](../deploy/compose.yaml)
- Backup and restore: [backup-restore.md](./backup-restore.md)
- Home Assistant summary endpoint: [home-assistant.md](./home-assistant.md)
