import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { ForbiddenComponent } from './forbidden.component';

describe('ForbiddenComponent', () => {
    let fixture: ComponentFixture<ForbiddenComponent>;
    let el: HTMLElement;

    beforeEach(async () => {
        await TestBed.configureTestingModule({
            imports: [ForbiddenComponent, getTranslocoTestingModule()],
            providers: [provideRouter([])],
        }).compileComponents();
        fixture = TestBed.createComponent(ForbiddenComponent);
        fixture.detectChanges();
        el = fixture.nativeElement as HTMLElement;
    });

    it('shows the 403 code, title, and reference', () => {
        expect(el.querySelector('.nw-error-page__code')?.textContent?.trim()).toBe('ERROR 403');
        expect(el.querySelector('h1')?.textContent?.trim()).toBe("You don't have access to this page");
        expect(el.querySelector('.nw-error-page__reference')?.textContent).toContain('403 forbidden');
    });

    it('offers a link home', () => {
        const home = el.querySelector('a.btn') as HTMLAnchorElement;
        expect(home.textContent?.trim()).toBe('Go to home');
        expect(home.getAttribute('href')).toBe('/');
    });
});
