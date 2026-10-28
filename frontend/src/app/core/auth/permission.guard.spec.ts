import { TestBed } from '@angular/core/testing';
import { ActivatedRouteSnapshot, Router, RouterStateSnapshot, UrlTree } from '@angular/router';
import { Observable, of } from 'rxjs';
import { anyPermissionGuard } from './permission.guard';
import { AuthService } from './auth.service';

describe('anyPermissionGuard', () => {
  let granted: string[];
  let isChild: boolean;
  let routerSpy: jasmine.SpyObj<Router>;
  const route = {} as ActivatedRouteSnapshot;
  const state = { url: '/settings/admin' } as RouterStateSnapshot;

  function run(...codes: string[]): Promise<boolean | UrlTree> {
    const guard = TestBed.runInInjectionContext(() => anyPermissionGuard(...codes)(route, state));
    return new Promise((resolve) => (guard as Observable<boolean | UrlTree>).subscribe(resolve));
  }

  beforeEach(() => {
    granted = [];
    isChild = true;
    routerSpy = jasmine.createSpyObj('Router', ['createUrlTree']);
    routerSpy.createUrlTree.and.callFake((commands: string[]) => ({ commands }) as unknown as UrlTree);
    TestBed.configureTestingModule({
      providers: [
        {
          provide: AuthService,
          useValue: {
            currentUser: () => ({}),
            checkAuth: () => of(true),
            loadPermissions: () => of(true),
            isAuthenticated: () => true,
            isChild: () => isChild,
            can: (code: string) => granted.includes(code),
          },
        },
        { provide: Router, useValue: routerSpy },
      ],
    });
  });

  it('should send a child without the permission to /today', async () => {
    const result = await run('settings.view');
    expect(result).toEqual({ commands: ['/today'] } as unknown as UrlTree);
  });

  it('should send a parent without the permission to /dashboard', async () => {
    isChild = false;
    const result = await run('settings.view');
    expect(result).toEqual({ commands: ['/dashboard'] } as unknown as UrlTree);
  });

  it('should allow a member who holds any one of the codes', async () => {
    granted = ['members.view'];
    expect(await run('members.view', 'family.view')).toBeTrue();
  });

  it('should not let a child open a parent-only route by URL', async () => {
    granted = ['tasks.own.view', 'rewards.view', 'calendar.own.view'];
    expect(await run('tasks.view')).not.toBeTrue();
    expect(await run('rewards.manage')).not.toBeTrue();
    expect(await run('rewards.approve')).not.toBeTrue();
    expect(await run('members.view', 'family.view')).not.toBeTrue();
  });
});
