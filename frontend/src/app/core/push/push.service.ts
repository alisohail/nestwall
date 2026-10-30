import { Injectable, inject } from '@angular/core';
import { SwPush } from '@angular/service-worker';
import { firstValueFrom } from 'rxjs';
import { ApiClient } from '../api/api-client.service';

interface PublicKeyResponse {
  public_key: string;
}

/**
 * Subscribes this browser to Web Push so family notices reach the phone's notification centre
 * even when the app is closed. Needs HTTPS and a granted notification permission.
 */
@Injectable({ providedIn: 'root' })
export class PushService {
  private swPush = inject(SwPush, { optional: true });
  private api = inject(ApiClient);

  /** False in development (no service worker) and in browsers without push support. */
  get supported(): boolean {
    return this.swPush?.isEnabled ?? false;
  }

  /** True when this browser already holds a push subscription. */
  async isSubscribed(): Promise<boolean> {
    if (!this.swPush || !this.supported) return false;
    return (await firstValueFrom(this.swPush.subscription)) !== null;
  }

  /** Creates or refreshes the subscription and stores it for the signed-in member. Returns success. */
  async subscribe(): Promise<boolean> {
    if (!this.swPush || !this.supported) return false;
    const { public_key } = await firstValueFrom(this.api.get<PublicKeyResponse>('/api/v1/push/public-key'));
    const subscription = await this.swPush.requestSubscription({ serverPublicKey: public_key });
    await firstValueFrom(this.api.post('/api/v1/push/subscribe', subscription.toJSON()));
    return true;
  }
}
