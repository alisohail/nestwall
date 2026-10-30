/** Address of the Android shell's setup screen. It lives on the shell's own origin. */
export const NATIVE_SERVER_PICKER_URL = 'https://localhost/?change=1';

/** True when this page runs inside the NestWall Android app. Remote server pages have no Capacitor bridge, so the WebView user agent carries a token. */
export function isNativeShell(): boolean {
  if (navigator.userAgent.includes('NestWallNative')) {
    return true;
  }
  const cap = (window as { Capacitor?: { isNativePlatform?: () => boolean } }).Capacitor;
  return cap?.isNativePlatform?.() === true;
}

interface NativeWatchPlugin {
  start(options: { serverUrl: string }): Promise<void>;
  stop(): Promise<void>;
}

/** Plugin methods are exposed on the Capacitor global, so no import is needed. */
function watchPlugin(): NativeWatchPlugin | undefined {
  const cap = (window as { Capacitor?: { Plugins?: { NestWallWatch?: NativeWatchPlugin } } }).Capacitor;
  return cap?.Plugins?.NestWallWatch;
}

/**
 * Starts the Android foreground watcher so alerts arrive while the app is closed.
 * Failures are ignored: the in-app bell still works without it.
 */
export function startNativeWatch(): void {
  watchPlugin()?.start({ serverUrl: location.origin }).catch(() => undefined);
}

/** Stops the watcher, for example before the user switches to another server. */
export function stopNativeWatch(): Promise<void> {
  return watchPlugin()?.stop().catch(() => undefined) ?? Promise.resolve();
}
