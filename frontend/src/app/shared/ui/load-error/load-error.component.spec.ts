import { ComponentFixture, TestBed } from '@angular/core/testing';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { LoadErrorComponent } from './load-error.component';

describe('LoadErrorComponent', () => {
    let fixture: ComponentFixture<LoadErrorComponent>;

    function setError(error: unknown): void {
        fixture.componentRef.setInput('error', error);
        fixture.detectChanges();
    }

    function title(): string {
        return (fixture.nativeElement as HTMLElement).querySelector('h1')?.textContent?.trim() ?? '';
    }

    beforeEach(async () => {
        await TestBed.configureTestingModule({
            imports: [LoadErrorComponent, getTranslocoTestingModule()],
        }).compileComponents();
        fixture = TestBed.createComponent(LoadErrorComponent);
    });

    it('explains a network failure when there is no HTTP status', () => {
        setError(null);
        expect(title()).toBe("Can't reach NestWall");
    });

    it('explains a server failure for 5xx responses and shows the code', () => {
        setError({ code: 'server_error', status: 500 });
        expect(title()).toBe('Something went wrong on our side');
        expect((fixture.nativeElement as HTMLElement).textContent).toContain('500');
    });

    it('explains missing access for 403', () => {
        setError({ code: 'forbidden', status: 403 });
        expect(title()).toBe("You don't have access");
    });

    it('explains a missing item for 404', () => {
        setError({ code: 'not_found', status: 404 });
        expect(title()).toBe('Not found');
    });

    it('emits retry when the user presses Try again', () => {
        setError({ code: 'server_error', status: 500 });
        const spy = vi.fn().mockName('retry');
        fixture.componentInstance.retry.subscribe(spy);
        (fixture.nativeElement as HTMLElement).querySelector<HTMLButtonElement>('[data-testid="load-error-retry"]')?.click();
        expect(spy).toHaveBeenCalled();
    });
});
