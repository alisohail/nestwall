/** The browser's position, as sent to the weather API. */
export interface DeviceCoords {
  latitude: number;
  longitude: number;
}

/** Why the browser did not give a position. Matches the settings.general.detect_error.* keys. */
export type DeviceLocationError = 'unsupported' | 'denied' | 'timeout' | 'unavailable';

/**
 * Asks the browser for the device position, rejecting with a DeviceLocationError. Browsers only
 * allow this on HTTPS or localhost, so over plain HTTP it rejects with 'unsupported'.
 */
export function currentDeviceLocation(): Promise<DeviceCoords> {
  return new Promise((resolve, reject) => {
    if (typeof navigator === 'undefined' || !navigator.geolocation) {
      reject('unsupported' satisfies DeviceLocationError);
      return;
    }
    navigator.geolocation.getCurrentPosition(
      (pos) => resolve({ latitude: pos.coords.latitude, longitude: pos.coords.longitude }),
      (err) => reject(geolocationErrorKind(err)),
      { enableHighAccuracy: false, timeout: 10000, maximumAge: 300000 },
    );
  });
}

function geolocationErrorKind(err: GeolocationPositionError): DeviceLocationError {
  switch (err.code) {
    case err.PERMISSION_DENIED:
      return 'denied';
    case err.TIMEOUT:
      return 'timeout';
    default:
      return 'unavailable';
  }
}
