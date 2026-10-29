import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { ServerErrorComponent } from './server-error.component';

describe('ServerErrorComponent', () => {
    let fixture: ComponentFixture<ServerErrorComponent>;
    let el: HTMLElement;

    beforeEach(async () => {
        await TestBed.configureTestingModule({
            imports: [ServerErrorComponent, getTranslocoTestingModule()],
            providers: [provideRouter([])],
        }).compileComponents();
        fixture = TestBed.createComponent(ServerErrorComponent);
        fixture.detectChanges();
        el = fixture.nativeElement as HTMLElement;
    });

    it('shows the 500 code, title, and reference', () => {
        expect(el.querySelector('.nw-error-page__code')?.textContent?.trim()).toBe('ERROR 500');
        expect(el.querySelector('h1')?.textContent?.trim()).toBe('Something went wrong on our side');
        expect(el.querySelector('.nw-error-page__reference')?.textContent).toContain('500 internal_error');
    });

    it('offers a try again button and a home link', () => {
        const buttons = Array.from(el.querySelectorAll('.btn'));
        expect(buttons.map(b => b.textContent?.trim())).toEqual(['Try again', 'Go to home']);
        expect(el.querySelector('a.btn')?.getAttribute('href')).toBe('/');
    });
});
