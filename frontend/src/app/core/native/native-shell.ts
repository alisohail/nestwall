/** Address of the Android shell's setup screen. It lives on the shell's own origin. */
export const NATIVE_SERVER_PICKER_URL = 'https://localhost/?change=1';

/** True when this page runs inside the NestWall Android app (Capacitor WebView). */
export function isNativeShell(): boolean {
  const cap = (window as { Capacitor?: { isNativePlatform?: () => boolean } }).Capacitor;
  return cap?.isNativePlatform?.() === true;
}
