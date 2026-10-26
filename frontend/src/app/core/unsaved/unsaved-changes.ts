import { Injectable, inject, signal } from '@angular/core';
import { CanDeactivateFn } from '@angular/router';

/**
 * Implemented by any page with editable values that are saved explicitly.
 * The route guard calls these when the user tries to leave the page.
 */
export interface UnsavedChangesAware {
  /** True when the page has edits that are not saved yet. */
  hasUnsavedChanges(): boolean;
  /** Saves the pending edits. Resolves true only when the save succeeded. */
  saveChanges(): Promise<boolean>;
}

export function isUnsavedChangesAware(value: unknown): value is UnsavedChangesAware {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as UnsavedChangesAware).hasUnsavedChanges === 'function' &&
    typeof (value as UnsavedChangesAware).saveChanges === 'function'
  );
}

interface PendingLeave {
  target: UnsavedChangesAware;
  resolve: (leave: boolean) => void;
}

/** Holds the open "unsaved changes" prompt. The dialog component reads it and reports the user's choice. */
@Injectable({
  providedIn: 'root',
})
export class UnsavedChangesService {
  readonly pending = signal<PendingLeave | null>(null);

  ask(target: UnsavedChangesAware): Promise<boolean> {
    return new Promise<boolean>((resolve) => this.pending.set({ target, resolve }));
  }

  /** Stay on the page. The navigation is cancelled. */
  stay(): void {
    this.close(false);
  }

  /** Redirect: leave the page and drop the unsaved edits. */
  leave(): void {
    this.close(true);
  }

  private close(leave: boolean): void {
    const current = this.pending();
    if (!current) return;
    this.pending.set(null);
    current.resolve(leave);
  }
}

/** Route guard. Blocks leaving a page that has unsaved edits until the user chooses what to do. */
export const unsavedChangesGuard: CanDeactivateFn<unknown> = (component) => {
  if (!isUnsavedChangesAware(component) || !component.hasUnsavedChanges()) return true;
  return inject(UnsavedChangesService).ask(component);
};
