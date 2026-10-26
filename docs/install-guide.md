# NestWall: install with Docker Compose (no coding needed)

This guide installs NestWall on any computer or home server that runs Docker. You download two small files, fill in a settings file, and start NestWall with one command. You do not need to clone the code or build anything.

If you are a developer who wants to build from source, use [dev-guide.md](./dev-guide.md) instead.

## What you need

- A computer or home server that stays on. Family members reach NestWall over your home network.
- Docker with the Compose plugin:
  - **Windows or macOS:** install [Docker Desktop](https://www.docker.com/products/docker-desktop/) and open it once so it is running.
  - **Linux:** install Docker Engine and the Compose plugin (`docker compose version` should print a version).
  - **Synology, QNAP and other NAS boxes:** use the Container Manager or Container Station app, then follow the [Portainer or NAS notes](#nas-and-portainer-notes) below.
- About 1 GB of free disk space for the program, plus room for the family data.
- The processor must be Intel or AMD (`amd64`). Most NAS boxes and PCs are. Raspberry Pi and Apple Silicon Macs are not supported by the published images yet.

NestWall runs as two containers:

| Container | Role | Port on your computer |
|---|---|---|
| `web` | The app the family uses in a browser | your `WEB_PORT` (default `5842`) |
| `api` | The server with the encrypted family database | not shared (only `web` talks to it) |

## Step 1: Create a folder and download two files

Create a folder called `nestwall` anywhere you like, for example `Documents\nestwall`. Put these two files in it:

1. **`compose.release.yaml`**: copy the file from the project's [compose.release.yaml](../compose.release.yaml) page on GitHub. Open the file, choose **Raw** and save it as `compose.release.yaml`.
2. **`.env`**: copy the file from the project's [.env.example](../.env.example) page on GitHub, save it in the same folder, and rename it to `.env`.

The file name must start with a dot and have no other name in front of it. On Windows, Notepad can save it as `.env` if you choose **All files** in the save dialog.

When you're done, the folder should look like this:

```
nestwall/
├── compose.release.yaml
└── .env
```

The `database` folder is created automatically on the first start.

## Step 2: Fill in the settings file

Open `.env` in a text editor. Change the values below. Keep the names on the left exactly as they are.

| Setting | What to put there |
|---|---|
| `DB_KEY` | A long passphrase that locks the family database. Use at least 24 random characters, for example from a password manager. **Without this passphrase the data cannot be opened, so save a copy somewhere safe.** |
| `SESSION_SECRET` | A second long random value (at least 48 characters) that keeps sign-ins secure. Use a password manager to generate it. |
| `APP_URL` | The address the family types into a browser, including the port, for example `http://192.168.1.20:5842`. Password reset links use it. Use `http://localhost:5842` if only this computer uses NestWall. |
| `WEB_PORT` | Optional. The port on your computer that opens NestWall. The default is `5842`, an uncommon port chosen to avoid clashes. Change it if another app uses that port too, and use the same port in `APP_URL`. |
| `TZ` | Your time zone, for example `Europe/London` or `America/New_York`. Scheduled backups run at the saved hour in this zone. |
| `NESTWALL_VERSION` | Optional. Leave it as `latest` or set a version such as `0.1.0` to stay on that release. Add the line if it is missing. |

Lines that start with `#` are notes and are ignored.

Do not share the `.env` file or post it online. It contains the keys that protect your data.

## Step 3: Start NestWall

Open a terminal in the `nestwall` folder:

- **Windows:** in File Explorer, click the address bar, type `cmd` and press Enter. Or open PowerShell in the folder.
- **macOS:** open Terminal, type `cd ` (with a space), drag the folder into the window and press Enter.
- **Linux or NAS SSH:** `cd` into the folder.

Then run:

```bash
docker compose -f compose.release.yaml up -d
```

The first start downloads the program. This takes a few minutes. Check that both containers are running:

```bash
docker compose -f compose.release.yaml ps
```

`web` should say `Up`, and `api` should say `healthy` (it can take about a minute).

## Step 4: Open NestWall

In a browser on the same network, open:

- `http://localhost:5842` on the computer that runs NestWall, or
- `http://<the computer's address>:5842` from a phone or tablet, for example `http://192.168.1.20:5842`.

The first screen is the setup screen. Follow it to create the first parent account, then sign in and add the family members.

To install NestWall on a phone or tablet, open the address in the browser and choose **Add to Home Screen** (or **Install app**).

## Everyday commands

Run these in the `nestwall` folder.

| What you want | Command |
|---|---|
| Start NestWall | `docker compose -f compose.release.yaml up -d` |
| Stop NestWall (data is kept) | `docker compose -f compose.release.yaml down` |
| See whether it is running | `docker compose -f compose.release.yaml ps` |
| See the log when something is wrong | `docker compose -f compose.release.yaml logs --tail 100 api` |

## Update NestWall

1. Check the [releases page](https://github.com/alisohail/nestwall/releases) for the new version number, for example `0.2.0`.
2. Open `.env` and set `NESTWALL_VERSION=0.2.0`. If you left it as `latest`, you can skip this step.
3. Run:

   ```bash
   docker compose -f compose.release.yaml pull
   docker compose -f compose.release.yaml up -d
   ```

Before you update, back up your data (see the next section). To go back to the old version, set `NESTWALL_VERSION` to the old number and repeat step 3.

## Back up your data

Your family data is in the `database` folder. The easiest backup is to:

1. Stop NestWall with `docker compose -f compose.release.yaml down`.
2. Copy the whole `database` folder to a safe place, such as an external drive or a cloud folder.
3. Start NestWall again with `docker compose -f compose.release.yaml up -d`.

NestWall also makes its own backups inside `database/backups` on a schedule. Keep a copy of `.env` too. Without `DB_KEY`, the backups cannot be opened. For restore steps, see [backup-restore.md](./backup-restore.md).

## NAS and Portainer notes

- **Portainer:** create a new stack, paste the contents of `compose.release.yaml`, and add the values from `.env` in the **Environment variables** section. Use a folder on the NAS as the stack's data location if it asks for one, because the `database` folder has to survive restarts.
- **Synology Container Manager:** create a project from the folder that holds `compose.release.yaml` and `.env`. Start it from the project page.
- Avoid changing the `database` folder while NestWall is running.
- **Port already in use on a NAS:** NAS systems and download or media apps can use common ports such as 8080. If the default `5842` is taken, pick another free port such as `8090` and set `WEB_PORT=8090`, then use `:8090` in `APP_URL` and in the browser address. Check the port first from the NAS terminal with `ss -ltn | grep :8090` (no output means it's free).

## Problems and fixes

| What you see | What to do |
|---|---|
| The page does not open | Check that `docker compose ... ps` shows `web` as `Up`. Check that the browser uses the same port as `WEB_PORT`. Check the firewall on the computer. |
| `port is already allocated` or `address already in use` when starting | Another app uses `WEB_PORT`. Pick another port (for example `8090`) in `.env`, set `APP_URL` to match, then run `docker compose -f compose.release.yaml up -d` again. To see what uses a port, use `netstat -ano \| findstr :5842` on Windows or `ss -ltn \| grep :5842` on Linux and NAS systems. |
| `api` never says `healthy` | Run `docker compose -f compose.release.yaml logs --tail 100 api`. Most often `DB_KEY` or `SESSION_SECRET` is missing from `.env`. |
| `pull` says `unauthorized` or `denied` | The published images must be public on GitHub. Ask the project owner, or run `docker login ghcr.io` with a GitHub account that can read them. |
| Phones cannot open the page | Use the computer's network address (not `localhost`) in `APP_URL` and in the browser. |
| The page looks out of date after an update | Close the browser tab and reopen it. If it still looks old, clear the site data for the address and reload. |

If something else goes wrong, copy the output of `docker compose -f compose.release.yaml logs --tail 100` and send it to the project owner. Do not send your `.env` file.
