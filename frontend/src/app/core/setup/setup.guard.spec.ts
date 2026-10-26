import { TestBed } from '@angular/core/testing';
import { ActivatedRouteSnapshot, Router, RouterStateSnapshot, UrlTree } from '@angular/router';
import { Observable, of, throwError } from 'rxjs';
import { setupGuard } from './setup.guard';
import { SetupService } from './setup.service';

describe('setupGuard', () => {
  let setupSpy: jasmine.SpyObj<SetupService>;
  let router: Router;

  beforeEach(() => {
    setupSpy = jasmine.createSpyObj('SetupService', ['getStatus']);
    TestBed.configureTestingModule({
      providers: [{ provide: SetupService, useValue: setupSpy }],
    });
    router = TestBed.inject(Router);
  });

  function run(): unknown {
    let result: unknown;
    const obs = TestBed.runInInjectionContext(() =>
      setupGuard({} as ActivatedRouteSnapshot, {} as RouterStateSnapshot)
    ) as Observable<unknown>;
    obs.subscribe((r) => (result = r));
    return result;
  }

  it('allows /setup while setup is required', () => {
    setupSpy.getStatus.and.returnValue(of({ setup_required: true }));
    expect(run()).toBe(true);
  });

  it('redirects to /login once a family exists', () => {
    setupSpy.getStatus.and.returnValue(of({ setup_required: false }));
    const result = run();
    expect(result instanceof UrlTree).toBeTrue();
    expect(router.serializeUrl(result as UrlTree)).toBe('/login');
  });

  it('redirects to /login when the status check fails', () => {
    setupSpy.getStatus.and.returnValue(throwError(() => new Error('down')));
    const result = run();
    expect(result instanceof UrlTree).toBeTrue();
    expect(router.serializeUrl(result as UrlTree)).toBe('/login');
  });
});
