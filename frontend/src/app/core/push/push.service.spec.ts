import type { Mock, MockedObject } from "vitest";
import { TestBed } from '@angular/core/testing';
import { SwPush } from '@angular/service-worker';
import { Observable, of } from 'rxjs';
import { mockOf } from '../../testing/mock-of';
import { ApiClient } from '../api/api-client.service';
import { PushService } from './push.service';

describe('PushService', () => {
    let api: MockedObject<ApiClient>;
    let swPush: { isEnabled: boolean; subscription: Observable<PushSubscription | null>; requestSubscription: Mock };

    beforeEach(() => {
        api = mockOf<ApiClient>({
            get: vi.fn().mockName('ApiClient.get'),
            post: vi.fn().mockName('ApiClient.post')
        });
        api.get.mockReturnValue(of({ public_key: 'PUBLIC' }));
        api.post.mockReturnValue(of(null));
        swPush = {
            isEnabled: true,
            subscription: of(null),
            requestSubscription: vi.fn().mockName('SwPush.requestSubscription').mockResolvedValue({
                toJSON: () => ({ endpoint: 'https://push.example/abc', keys: { p256dh: 'P', auth: 'A' } })
            })
        };
        TestBed.configureTestingModule({
            providers: [{ provide: ApiClient, useValue: api }, { provide: SwPush, useValue: swPush }]
        });
    });

    it('subscribes with the server key and stores the subscription', async () => {
        const service = TestBed.inject(PushService);

        await expect(service.subscribe()).resolves.toBe(true);

        expect(swPush.requestSubscription).toHaveBeenCalledWith({ serverPublicKey: 'PUBLIC' });
        expect(api.post).toHaveBeenCalledWith('/api/v1/push/subscribe', {
            endpoint: 'https://push.example/abc',
            keys: { p256dh: 'P', auth: 'A' }
        });
    });

    it('does nothing when push is not enabled', async () => {
        swPush.isEnabled = false;
        const service = TestBed.inject(PushService);

        await expect(service.subscribe()).resolves.toBe(false);
        expect(swPush.requestSubscription).not.toHaveBeenCalled();
    });

    it('reports no subscription until the browser has one', async () => {
        const service = TestBed.inject(PushService);
        await expect(service.isSubscribed()).resolves.toBe(false);

        swPush.subscription = of({ endpoint: 'x' } as PushSubscription);
        await expect(service.isSubscribed()).resolves.toBe(true);
    });
});
