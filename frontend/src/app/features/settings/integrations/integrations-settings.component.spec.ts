import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Observable, of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { AuthService } from '../../../core/auth/auth.service';
import { ApiError } from '../../../core/api/api-client.service';
import { mockOf } from '../../../testing/mock-of';
import { ApiTokenCreated, ApiTokenView, ApiTokensService } from './api-tokens.service';
import { IntegrationsSettingsComponent } from './integrations-settings.component';

function token(overrides: Partial<ApiTokenView> = {}): ApiTokenView {
    return {
        id: 't1',
        name: 'Home Assistant',
        scopes: ['tasks:read', 'calendar:read'],
        token_hint: 'nst_8f3k…q2',
        expires_at: new Date(Date.now() + 330 * 864e5).toISOString(),
        last_used_at: new Date(Date.now() - 3 * 6e4).toISOString(),
        created_at: new Date().toISOString(),
        ...overrides,
    };
}

describe('IntegrationsSettingsComponent', () => {
    let fixture: ComponentFixture<IntegrationsSettingsComponent>;
    let component: IntegrationsSettingsComponent;
    let api: MockedObject<ApiTokensService>;
    let grants: string[];

    function el<T extends Element = HTMLElement>(selector: string): T | null {
        return fixture.nativeElement.querySelector(selector) as T | null;
    }

    function build(): void {
        fixture = TestBed.createComponent(IntegrationsSettingsComponent);
        component = fixture.componentInstance;
        fixture.detectChanges();
    }

    beforeEach(() => {
        grants = ['settings.view', 'settings.manage'];
        api = mockOf<ApiTokensService>({
            list: vi.fn().mockName("ApiTokensService.list"),
            create: vi.fn().mockName("ApiTokensService.create"),
            revoke: vi.fn().mockName("ApiTokensService.revoke")
        });
        api.list.mockReturnValue(of([token()]));
        api.revoke.mockReturnValue(of(undefined));

        TestBed.configureTestingModule({
            imports: [IntegrationsSettingsComponent, getTranslocoTestingModule()],
            providers: [
                { provide: ApiTokensService, useValue: api },
                {
                    provide: AuthService,
                    useValue: {
                        can: (perm: string) => grants.includes(perm),
                        loadPermissions: (): Observable<void> => of(undefined),
                    },
                },
            ],
        });
    });

    it('shows the load error screen and retry reloads the tokens', () => {
        api.list.mockReturnValue(throwError(() => ({ code: 'network', status: 0 }) as ApiError));
        build();

        expect(el('app-loading')).toBeNull();
        expect(el('app-load-error')).not.toBeNull();

        api.list.mockReturnValue(of([token()]));
        el<HTMLButtonElement>('[data-testid="load-error-retry"]')?.click();
        fixture.detectChanges();

        expect(api.list).toHaveBeenCalledTimes(2);
        expect(el('app-load-error')).toBeNull();
    });

    it('shows a permission banner and does not load tokens without settings.view', () => {
        grants = [];
        build();

        expect(el('app-banner')).not.toBeNull();
        expect(api.list).not.toHaveBeenCalled();
    });

    it('lists tokens with scopes, last used and expiry', () => {
        build();

        expect(api.list).toHaveBeenCalled();
        expect(fixture.nativeElement.querySelectorAll('table.tokens tbody tr').length).toBe(1);
        expect(fixture.nativeElement.querySelectorAll('table.tokens .badge').length).toBe(2);
        expect(fixture.nativeElement.textContent).toContain('Home Assistant');
        expect(fixture.nativeElement.textContent).toContain('3 minutes ago');
        expect(fixture.nativeElement.textContent).toContain('In 11 months');
    });

    it('shows the Home Assistant guide with the summary endpoint and the bearer secret', () => {
        build();

        const yaml = el('pre.guide-yaml')?.textContent ?? '';
        expect(yaml).toContain('/api/v1/integrations/summary');
        expect(yaml).toContain('Authorization: !secret nestwall_bearer');
        expect(yaml).toContain('{{ value_json.data.tasks_due_today }}');
    });

    it('shows Never for a token that has not been used', () => {
        api.list.mockReturnValue(of([token({ last_used_at: null })]));
        build();

        expect(fixture.nativeElement.textContent).toContain('Never');
    });

    it('hides create and revoke without settings.manage', () => {
        grants = ['settings.view'];
        api.list.mockReturnValue(of([token()]));
        build();

        expect(el('.section-head button')).toBeNull();
        expect(el('table.tokens button.danger')).toBeNull();
    });

    it('creates a token with the chosen scopes and expiry, and shows it once', () => {
        const created: ApiTokenCreated = { ...token({ id: 't2', name: 'Grafana' }), token: 'nst_secret_value' };
        api.create.mockReturnValue(of(created));
        build();

        component.openNew();
        component.name.set('Grafana');
        component.toggleScope('points:read');
        component.toggleScope('calendar:read');
        component.expiryDays.set(90);
        component.create();

        expect(api.create).toHaveBeenCalledWith({
            name: 'Grafana',
            scopes: ['tasks:read', 'points:read'],
            expires_in_days: 90,
        });
        fixture.detectChanges();
        expect(el('.token-value')?.textContent).toContain('nst_secret_value');
    });

    it('does not submit without a name', () => {
        build();
        component.openNew();
        component.name.set('   ');
        component.create();

        expect(api.create).not.toHaveBeenCalled();
        expect(component.canSubmit()).toBe(false);
    });

    it('shows the server error when create fails', () => {
        api.create.mockReturnValue(throwError(() => ({ code: 'validation_failed' }) as ApiError));
        build();
        component.openNew();
        component.name.set('Grafana');
        component.create();
        fixture.detectChanges();

        expect(el('app-banner')).not.toBeNull();
        expect(component.creating()).toBe(false);
    });

    it('revokes after confirmation and removes the row', () => {
        build();
        component.askRevoke(token());
        fixture.detectChanges();
        expect(el('.confirm')).not.toBeNull();

        component.revoke(token());
        fixture.detectChanges();

        expect(api.revoke).toHaveBeenCalledWith('t1');
        expect(fixture.nativeElement.querySelectorAll('table.tokens tbody tr').length).toBe(0);
        expect(component.notice()).toBe('Home Assistant');
    });
});
