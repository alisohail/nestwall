import { TestBed } from '@angular/core/testing';
import { BusyService } from './busy.service';

const flushTimers = () => new Promise((resolve) => setTimeout(resolve));

describe('BusyService', () => {
    let busy: BusyService;

    beforeEach(() => {
        TestBed.configureTestingModule({});
        busy = TestBed.inject(BusyService);
    });

    it('is busy while any save is pending', () => {
        busy.begin();
        busy.begin();
        expect(busy.busy()).toBe(true);

        busy.end(false);
        expect(busy.busy()).toBe(true);

        busy.end(false);
        expect(busy.busy()).toBe(false);
    });

    it('never goes below zero', () => {
        busy.end(false);
        expect(busy.busy()).toBe(false);
    });

    it('copies the message from the page after a failed save', async () => {
        const alert = document.createElement('p');
        alert.setAttribute('role', 'alert');
        alert.textContent = 'Email already taken';
        alert.scrollIntoView = vi.fn();
        document.body.appendChild(alert);

        busy.begin();
        busy.end(true);
        await flushTimers();

        expect(busy.saveFailure()).toEqual({ message: 'Email already taken' });
        alert.remove();
    });

    it('falls back to a generic message when the page shows no error', async () => {
        busy.begin();
        busy.end(true);
        await flushTimers();

        expect(busy.saveFailure()).toEqual({ key: 'common.save_failed' });
    });

    it('clears the failure when the next save starts or is dismissed', async () => {
        busy.begin();
        busy.end(true);
        await flushTimers();
        busy.begin();
        expect(busy.saveFailure()).toBeNull();

        busy.begin();
        busy.end(true);
        await flushTimers();
        busy.dismissFailure();
        expect(busy.saveFailure()).toBeNull();
    });
});
