import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { AuthService } from './auth.service';

describe('AuthService permissions', () => {
  let service: AuthService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    service = TestBed.inject(AuthService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it('should grant only the codes returned by the server', () => {
    service.loadPermissions().subscribe();
    http
      .expectOne((r) => r.url.endsWith('/api/v1/auth/permissions'))
      .flush({ permissions: ['tasks.view', 'tasks.complete'] });

    expect(service.can('tasks.complete')).toBeTrue();
    expect(service.can('tasks.view')).toBeTrue();
    expect(service.can('tasks.manage')).toBeFalse();
  });

  it('should fail closed when the permissions request fails', () => {
    service.loadPermissions().subscribe();
    http
      .expectOne((r) => r.url.endsWith('/api/v1/auth/permissions'))
      .flush({ error: { code: 'internal_error', message: 'boom' } }, { status: 500, statusText: 'Server Error' });

    expect(service.can('tasks.view')).toBeFalse();
    expect(service.can('tasks.complete')).toBeFalse();
  });

  it('should deny everything before permissions are loaded', () => {
    expect(service.can('tasks.manage')).toBeFalse();
  });
});
