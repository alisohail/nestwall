import { Component, computed, forwardRef, input, signal } from '@angular/core';
import { ControlValueAccessor, NG_VALUE_ACCESSOR } from '@angular/forms';
import { TranslocoModule } from '@ngneat/transloco';

import { isLowContrast } from './colour-contrast';

/** Quick-pick colours used everywhere a colour is chosen. Hex values, because the backend stores them as #RRGGBB. */
export const COLOUR_PALETTE: readonly string[] = ['#119e8f', '#c47e09', '#ec4899', '#6366f1', '#0d96d4', '#649b11', '#ef4444', '#8b5cf6'];

/**
 * Colour choice with quick-pick swatches and a native picker for any RGB colour.
 * Works with ngModel and reactive forms. A value outside the palette shows on the picker cell.
 * With checkContrast set, a colour too faint on the light or dark surface shows a warning.
 */
@Component({
  selector: 'app-colour-picker',
  standalone: true,
  imports: [TranslocoModule],
  templateUrl: './colour-picker.component.html',
  styleUrl: './colour-picker.component.scss',
  providers: [{ provide: NG_VALUE_ACCESSOR, useExisting: forwardRef(() => ColourPickerComponent), multi: true }],
})
export class ColourPickerComponent implements ControlValueAccessor {
  readonly label = input.required<string>();
  readonly palette = input<readonly string[]>(COLOUR_PALETTE);
  readonly checkContrast = input(false);

  readonly value = signal('');
  readonly disabled = signal(false);

  /** The swatch matching the value, ignoring case. Null when the value is outside the palette. */
  readonly paletteMatch = computed(() => {
    const value = this.value().toLowerCase();
    return this.palette().find((c) => c.toLowerCase() === value) ?? null;
  });
  /** A colour outside the palette, shown on the picker cell. Null when a swatch or nothing is selected. */
  readonly customColour = computed(() => (this.paletteMatch() || !this.value() ? null : this.value()));
  /** True when checkContrast is set and the value is too faint on a light or dark card. */
  readonly lowContrast = computed(() => this.checkContrast() && isLowContrast(this.value()));
  /** The picker cell shows this rainbow until a custom colour is chosen. */
  readonly rainbow = computed(() => {
    const colours = this.palette();
    return `conic-gradient(${[...colours, colours[0]].join(', ')})`;
  });

  private onChange: (value: string) => void = () => undefined;
  private onTouched: () => void = () => undefined;

  writeValue(value: string | null): void {
    this.value.set(value ?? '');
  }

  registerOnChange(fn: (value: string) => void): void {
    this.onChange = fn;
  }

  registerOnTouched(fn: () => void): void {
    this.onTouched = fn;
  }

  setDisabledState(isDisabled: boolean): void {
    this.disabled.set(isDisabled);
  }

  choose(colour: string): void {
    this.commit(colour);
  }

  /** Takes any colour from the native picker. A value that matches a swatch selects that swatch. */
  pickCustom(value: string): void {
    this.commit(value);
  }

  private commit(colour: string): void {
    this.value.set(colour);
    this.onChange(colour);
    this.onTouched();
  }
}
