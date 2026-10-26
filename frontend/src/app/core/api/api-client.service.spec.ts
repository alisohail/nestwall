import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ApiClient } from './api-client.service';
import { BusyService } from '../busy/busy.service';

describe('ApiClient busy tracking', () => {
  let api: ApiClient;
  let http: HttpTestingController;
  let busy: BusyService;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    api = TestBed.inject(ApiClient);
    http = TestBed.inject(HttpTestingController);
    busy = TestBed.inject(BusyService);
  });

  afterEach(() => http.verify());

  it('shows busy while a save is in flight and clears it on success', () => {
    let result: unknown;
    api.post('/api/v1/things', {}).subscribe((r) => (result = r));

    expect(busy.busy()).toBe(true);
    http.expectOne('/api/v1/things').flush({ ok: true });

    expect(result).toEqual({ ok: true });
    expect(busy.busy()).toBe(false);
  });

  it('clears busy after a failed save', () => {
    let failed = false;
    api.put('/api/v1/things/1', {}).subscribe({ error: () => (failed = true) });

    expect(busy.busy()).toBe(true);
    http.expectOne('/api/v1/things/1').flush({ code: 'boom' }, { status: 500, statusText: 'Server Error' });

    expect(failed).toBe(true);
    expect(busy.busy()).toBe(false);
  });

  it('does not show busy for silent writes', () => {
    api.put('/api/v1/auth/me/theme', { theme: 'dark' }, undefined, { silent: true }).subscribe();

    expect(busy.busy()).toBe(false);
    http.expectOne('/api/v1/auth/me/theme').flush(null);
  });

  it('does not start a save until it is subscribed', () => {
    api.delete('/api/v1/things/2');
    expect(busy.busy()).toBe(false);
  });
});
