# NestWall Backup and Restore Runbook

This runbook covers in-app backups (M7-10 to M7-12, hardened in M8-01), the scheduled and retention rules (M8-04), and how to restore from the app or from the command line. Export files from **Settings > Data** are for reading only and are not backups.

## What a backup contains

A backup is one zip archive named `nestwall-YYYYMMDD-HHMMSS.zip`. It holds a consistent copy of the SQLite database and the avatar files, so the two always match. Backups are written to `BACKUP_DIR` (default `database/backups`; in Docker `/app/database/backups`, which is `database/backups/` on the host).

- **Encrypted** archives use AES-256-GCM with a key from scrypt. The passphrase is required to open them. Set it in **Settings > Data > Backup encryption**.
- **Unencrypted** archives can be read by anyone with the file. Keep them as private as the database.
- Every archive records the NestWall schema version. An archive from a newer version is refused, so an older install never runs a database it cannot read.

## Scheduled backups and retention

- The scheduler runs once per day. On API start, if no backup has run recently, it runs one straight away.
- Each run is recorded in backup history. A scheduled or manual run keeps the newest 6 archives. Older ones are pruned, and only archives this app created are touched.
- A reset run (`before_reset`) is kept for 30 days from its archive date, then removed.
- If a scheduled run fails, every parent is notified.

## Create a backup in the app

1. Sign in as a parent with `settings.manage` (parents only; children cannot see backups).
2. In the Backups card, select **Back up now**.
3. The run appears in history with its size and encryption state. Use **Download** to keep a copy off the server. Downloading needs the parent's password again.

Turning on **Encrypt backups** needs a passphrase. Store it in a password manager. An encrypted backup cannot be opened without it.

## Restore in the app

1. Open **Settings > Data**, go to the Backups card, choose a run (or upload an archive), and select **Restore**.
2. Enter the parent's password. If the form asks for the archive passphrase, enter it.
3. The restore checks the archive before it touches anything. A wrong passphrase or a corrupt archive changes nothing.
4. On success the data is replaced and the API asks for a restart. The restore writes a `backup.restored` audit row into the restored database, only if the parent who started it exists in the archive.

## Restore from the command line

Use this when the app cannot start or when you rebuild on a new host. Stop the API first. Restore replaces the database and exits, so Docker restarts the API afterwards.

```bash
# Same DB_PATH and DB_KEY the API uses
export DB_PATH=/app/database/nestwall.db
export DB_KEY='<the database key>'
export NESTWALL_RESTORE_PASSPHRASE='<backup passphrase, if encrypted>'

/app/api restore /app/database/backups/nestwall-20261022-222354.zip
# then start the API again
```

Output on success is the count of users, family members and ledger entries in the archive, then "Start the API again". A wrong passphrase prints "backup is corrupt, or the passphrase is wrong" and exits 1, and the database is not created or changed.

## First-run restore (new install)

On an empty install, the setup screen offers **Restore from a backup**, which posts to `POST /api/v1/setup/restore`. It is public but refuses as soon as any family exists. Use it when moving to a new host: upload the archive and enter its passphrase if it is encrypted.

## Keys and files you must keep

- **`DB_KEY`** (in `deploy/.env`): the database cannot be read without it. Keep a copy outside the repo. A backup is made with the key in use at the time, so restore the matching key too.
- **The backup passphrase**: needed for encrypted archives only.
- Keep at least one copy of the archives off the machine that runs the API.

## Verification after a restore

1. Sign in as a parent with the password from the backup point.
2. Check family members, tasks, points and calendar events against what you expect.
3. Open **Settings > Data > Backups** and confirm the history.

## Drill record

### 2026-10-22, same machine, temporary folder (M8-04)

Done with a locally built API binary, a seeded demo database, and a temporary folder. All temp files and keys were deleted afterwards. The live Docker stack was not used.

| Step | Result |
|---|---|
| Scheduled run on API start | Created an unencrypted archive, 1,286,824 bytes |
| Manual run with encryption on | Verified and encrypted, 1,286,872 bytes, 6 members in source |
| CLI restore, wrong passphrase | Exit 1, message "backup is corrupt, or the passphrase is wrong", no database created |
| CLI restore, right passphrase | Exit 0, "6 users, 6 family members, 61 ledger entries" |
| Restored API | Parent sign-in works with the backup's password; 6 members |
| Backup history after restore | 1 row (the scheduled run). The source had 2, because the manual run's own row is written after its archive is taken, so an archive never contains its own run. Expected behaviour. |

Covered by unit tests, not run live in this drill: newer-version archive refused (`TestCheckVersionsRefusesNewerBackups`), failed scheduled run notifies parents (`TestSchedulerFailureNotifiesParents`), retention (`TestKeepNewestCount`, `TestApplyRetentionOnlyTouchesOwnArchives`), first-run restore (`TestSetupRestoreOnEmptyInstallReplacesData`), and the restore round trip (`TestRestoreRoundTripOnCleanMachine`).

### 2026-10-22, fresh Docker volume on the same host (M8-04 acceptance)

The user chose a fresh Docker volume over a second machine. The live stack on port 8080 was not touched.

| Step | Result |
|---|---|
| Source archive | `nestwall-20261022-152823.zip` from the live stack (unencrypted, 1,382,360 bytes) |
| Fresh volume | Empty named volume, archive copied in with the API image |
| CLI restore | Exit 0, "6 users, 6 family members, 63 ledger entries" (archive header) |
| Restored API | Healthy on a separate port, reset `sam`'s password with `api reset-password`, signed in, changed the password |
| Members after restore | 6 (Sam, Jordan, Nana, Ava, Mia, Noah) |
| Backup history after restore | 1 row (the archive's scheduled run; the manual run's row is written after its archive, as above) |
| Cleanup | Container and volume removed; cookie file removed |

Notes:
- The archive `nestwall-20261022-162142.zip` (and `160317`) holds 0 users. These were taken after the factory reset of 12:02 (`nestwall-reset-20261022-160216.zip`), so they are empty. Restoring one gives an empty install. Pick the newest archive that has data.
- The restore and sign-in used a password reset for the drill account. That is a drill step, not something a real family would do; a real restore keeps the password from the backup point.
- The drill compares counts against the archive header, not against a live query before the restore. A live count before the backup was not recorded.

## Troubleshooting

- **Restore refused, passphrase wrong**: check `NESTWALL_RESTORE_PASSPHRASE` has no extra spaces or quotes.
- **Restore refused, newer version**: the archive was made by a newer NestWall. Upgrade the API first.
- **Cannot open the database**: `DB_KEY` must match the key used when the backup was made.
- **Every request returns 403 `password_change_required`**: the parent must change the password first (`POST /api/v1/auth/change-password`).
- **Scheduled backups missing**: check the history for a failed run; parents are notified of failures.
