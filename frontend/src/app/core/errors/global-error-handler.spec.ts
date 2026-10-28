import type { Mock } from "vitest";
import { TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { GlobalErrorHandler } from './global-error-handler';

describe('GlobalErrorHandler', () => {
    let handler: GlobalErrorHandler;
    let navigateSpy: Mock;

    beforeEach(() => {
        vi.spyOn(console, 'error').mockReturnValue(undefined);
        TestBed.configureTestingModule({
            providers: [provideRouter([]), GlobalErrorHandler],
        });
        handler = TestBed.inject(GlobalErrorHandler);
        navigateSpy = vi.spyOn(TestBed.inject(Router), 'navigateByUrl').mockResolvedValue(true);
    });

    it('opens the forbidden page for HTTP 403', () => {
        handler.handleError({ code: 'forbidden', status: 403 });
        expect(navigateSpy).toHaveBeenCalledWith('/forbidden');
    });

    it('opens the server-error page for HTTP 5xx', () => {
        handler.handleError({ code: 'internal_error', status: 500 });
        expect(navigateSpy).toHaveBeenCalledWith('/server-error');
    });

    it('does not navigate for 404 or network errors', () => {
        handler.handleError({ code: 'not_found', status: 404 });
        handler.handleError({ status: 0 });
        expect(navigateSpy).not.toHaveBeenCalled();
    });

    it('logs but does not navigate for non-HTTP errors', () => {
        handler.handleError(new Error('ui glitch'));
        expect(console.error).toHaveBeenCalled();
        expect(navigateSpy).not.toHaveBeenCalled();
    });
});
