# Android app setup

The Android app is a thin native shell. On first launch it asks for your NestWall server address (`https://` or `http://`), then loads that server in a full-screen WebView. The app's Settings page has a **Change server address** row to point it at a different instance.

This guide covers building the debug APK, installing it, and the prerequisites. The shell lives in [mobile/](../mobile/).

## What is and isn't in the app yet

- Done: server-address screen, full-screen WebView, change-server row (Settings > System, shown only inside the app), background alerts (see Notifications below).
- Not yet: Firebase push and Play Store upload automation. Alerts currently come from the background watcher, not from Google's push service.

## Prerequisites

| Tool | Version | Notes |
| --- | --- | --- |
| Node.js | 20 or newer | Installs the Capacitor CLI |
| JDK | 21 | Set `JAVA_HOME` to the JDK folder |
| Android SDK | platform 36, build-tools 36.0.0, platform-tools | Set `ANDROID_HOME` |

On Windows, the SDK normally lives at `%LOCALAPPDATA%\Android\Sdk`. Install the command-line tools, then:

```powershell
sdkmanager "platform-tools" "platforms;android-36" "build-tools;36.0.0"
sdkmanager --licenses
```

Android Studio also works and installs the same packages.

## Build the debug APK

Run these from the repository root.

```powershell
cd mobile
npm install
npx cap sync android
cd android
$env:JAVA_HOME = "C:\Program Files\Microsoft\jdk-21.0.13.21-hotspot"
$env:ANDROID_HOME = "$env:LOCALAPPDATA\Android\Sdk"
.\gradlew.bat assembleDebug
```

Output: `mobile/android/app/build/outputs/apk/debug/app-debug.apk`.

Run `npx cap sync android` again after changing [capacitor.config.json](../mobile/capacitor.config.json) or the files in `mobile/www/`.

To install on a phone with USB debugging enabled:

```powershell
adb install -r app\build\outputs\apk\debug\app-debug.apk
```

## First launch

1. Open NestWall. Enter your server address, for example `https://nestwall.example.com` or a LAN address such as `http://192.168.1.10:8080`.
2. The app saves the address and loads the server. On later launches it goes straight to the saved server.
3. To switch servers, open **Settings > System > Change server address**.

Plain `http://` sends the login and session cookie unencrypted, so use it only on a network you trust. Use HTTPS for any server reachable from the internet. Browser bell alerts still need HTTPS (see [install-guide.md](./install-guide.md#https-and-phone-notifications)). The native watcher works over either.

## Notifications

Once you open the app and sign in, it starts a foreground watcher. The watcher keeps a connection to your server open, so alerts such as a new task submission or a reward request arrive even when the app is closed. Android shows a quiet, persistent "Watching for new NestWall alerts" notice while it runs. This needs no Firebase project and no keys.

- The watcher starts when the app opens. It does not restart after a phone reboot until you open the app again.
- Tapping the notice or an alert opens the app.
- Alerts follow the same on/off setting as the in-app bell.
- Changing the server stops the old watcher, and the new server's watcher starts when its page loads.
- Android 15 and later require a special-use foreground service. For Play Console, declare it as "Keeping a connection to a self-hosted family server so members get alerts while the app is closed".

## Troubleshooting

- **Gradle cannot find the SDK.** Check `ANDROID_HOME`, or add `mobile/android/local.properties` with `sdk.dir=C:\\Users\\<you>\\AppData\\Local\\Android\\Sdk`. The file is git-ignored.
- **Gradle cannot find Java.** Check `JAVA_HOME` points to JDK 21, not a Java 8 or 17 install.
- **The server link opens in the browser instead of the app.** The config must keep `server.allowNavigation: ["*"]`. Run `npx cap sync android` after editing it.
- **Blank page after entering a server.** Confirm the address opens in Chrome on the phone and the certificate is trusted.

## Signing a release build

Release builds need a keystore. Keep it out of git.

1. Create the keystore once, and store it somewhere safe outside the repository:

   ```powershell
   keytool -genkeypair -v -keystore nestwall-release.jks -alias nestwall -keyalg RSA -keysize 2048 -validity 10000
   ```

2. Back up the keystore and its passwords. Google Play needs the same signing key for every future update.
3. Build with `.\gradlew.bat bundleRelease` to produce an `.aab` for Play Console.

The Gradle project does not read the keystore yet. Add a `signingConfigs` block in `mobile/android/app/build.gradle` that reads the keystore path and passwords from environment variables or `local.properties`, then point the release build type at it. Without it, `bundleRelease` produces an unsigned bundle that Play rejects.

The `*.jks` and `*.keystore` patterns are not ignored in `.gitignore`, so never commit them.

## Google Play upload

The first upload is manual: create the app in Play Console, upload the `.aab`, and complete the store listing. After that, release automation can upload new versions. That workflow is not added yet.
