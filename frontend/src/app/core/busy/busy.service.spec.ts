import { TestBed, fakeAsync, tick } from '@angular/core/testing';
import { BusyService } from './busy.service';

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

  it('copies the message from the page after a failed save', fakeAsync(() => {
    const alert = document.createElement('p');
    alert.setAttribute('role', 'alert');
    alert.textContent = 'Email already taken';
    document.body.appendChild(alert);

    busy.begin();
    busy.end(true);
    tick();

    expect(busy.saveFailure()).toEqual({ message: 'Email already taken' });
    alert.remove();
  }));

  it('falls back to a generic message when the page shows no error', fakeAsync(() => {
    busy.begin();
    busy.end(true);
    tick();

    expect(busy.saveFailure()).toEqual({ key: 'common.save_failed' });
  }));

  it('clears the failure when the next save starts or is dismissed', fakeAsync(() => {
    busy.begin();
    busy.end(true);
    tick();
    busy.begin();
    expect(busy.saveFailure()).toBeNull();

    busy.begin();
    busy.end(true);
    tick();
    busy.dismissFailure();
    expect(busy.saveFailure()).toBeNull();
  }));
});
