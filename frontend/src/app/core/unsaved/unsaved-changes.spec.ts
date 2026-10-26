import { TestBed } from '@angular/core/testing';
import { UnsavedChangesAware, UnsavedChangesService, isUnsavedChangesAware, unsavedChangesGuard } from './unsaved-changes';

function page(dirty: boolean, saveResult = true): UnsavedChangesAware & { saveChanges: jasmine.Spy } {
  return {
    hasUnsavedChanges: () => dirty,
    saveChanges: jasmine.createSpy('saveChanges').and.resolveTo(saveResult),
  };
}

describe('UnsavedChangesService', () => {
  let service: UnsavedChangesService;

  beforeEach(() => {
    TestBed.configureTestingModule({});
    service = TestBed.inject(UnsavedChangesService);
  });

  it('lets the user stay on the page', async () => {
    const result = service.ask(page(true));
    expect(service.pending()).not.toBeNull();
    service.stay();
    expect(await result).toBe(false);
    expect(service.pending()).toBeNull();
  });

  it('lets the user redirect without saving', async () => {
    const target = page(true);
    const result = service.ask(target);
    service.leave();
    expect(await result).toBe(true);
    expect(target.saveChanges).not.toHaveBeenCalled();
  });
});

describe('unsavedChangesGuard', () => {
  beforeEach(() => {
    TestBed.configureTestingModule({});
  });

  it('allows leaving when the page is clean', () => {
    const allowed = TestBed.runInInjectionContext(() => unsavedChangesGuard(page(false), {} as never, {} as never, {} as never));
    expect(allowed).toBe(true);
  });

  it('allows leaving pages that do not track changes', () => {
    const allowed = TestBed.runInInjectionContext(() => unsavedChangesGuard({}, {} as never, {} as never, {} as never));
    expect(allowed).toBe(true);
  });

  it('asks the user when the page has unsaved changes', () => {
    const service = TestBed.inject(UnsavedChangesService);
    const result = TestBed.runInInjectionContext(() => unsavedChangesGuard(page(true), {} as never, {} as never, {} as never));
    expect(result).toBeInstanceOf(Promise);
    expect(service.pending()).not.toBeNull();
    service.stay();
  });

  it('detects the unsaved-changes contract', () => {
    expect(isUnsavedChangesAware(page(false))).toBe(true);
    expect(isUnsavedChangesAware({ hasUnsavedChanges: () => true })).toBe(false);
  });
});
