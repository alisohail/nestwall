import { ComponentFixture, TestBed } from '@angular/core/testing';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { ErrorStateComponent } from './error-state.component';

describe('ErrorStateComponent', () => {
    let fixture: ComponentFixture<ErrorStateComponent>;
    let el: HTMLElement;

    beforeEach(async () => {
        await TestBed.configureTestingModule({
            imports: [ErrorStateComponent, getTranslocoTestingModule()],
        }).compileComponents();
        fixture = TestBed.createComponent(ErrorStateComponent);
        fixture.componentRef.setInput('messageKey', 'errors.messages.network');
        fixture.detectChanges();
        el = fixture.nativeElement as HTMLElement;
    });

    it('shows the message as an alert', () => {
        expect(el.querySelector('[data-testid="error-state-message"]')?.textContent).toContain('Check your connection');
        expect(el.querySelector('[role="alert"]')).not.toBeNull();
    });

    it('emits retry when the retry button is pressed', () => {
        const spy = vi.spyOn(fixture.componentInstance.retry, 'emit').mockReturnValue(undefined);
        (el.querySelector('[data-testid="error-state-retry"]') as HTMLButtonElement).click();
        expect(spy).toHaveBeenCalled();
    });

    it('names the icon-only retry button with the default label', () => {
        const button = el.querySelector('[data-testid="error-state-retry"]');
        expect(button?.getAttribute('aria-label')).toBe('Try again');
    });
});
