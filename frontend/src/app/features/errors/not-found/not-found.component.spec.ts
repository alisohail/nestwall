import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { NotFoundComponent } from './not-found.component';

describe('NotFoundComponent', () => {
    let fixture: ComponentFixture<NotFoundComponent>;
    let el: HTMLElement;

    beforeEach(async () => {
        await TestBed.configureTestingModule({
            imports: [NotFoundComponent, getTranslocoTestingModule()],
            providers: [provideRouter([])],
        }).compileComponents();
        fixture = TestBed.createComponent(NotFoundComponent);
        fixture.detectChanges();
        el = fixture.nativeElement as HTMLElement;
    });

    it('shows the 404 code and title', () => {
        expect(el.querySelector('.nw-error-page__code')?.textContent?.trim()).toBe('ERROR 404');
        expect(el.querySelector('h1')?.textContent?.trim()).toBe("We can't find that page");
    });

    it('links back home', () => {
        expect(el.querySelector('a.btn')?.getAttribute('href')).toBe('/');
    });
});
