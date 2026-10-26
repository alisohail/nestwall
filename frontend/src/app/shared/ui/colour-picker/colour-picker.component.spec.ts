import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormControl, ReactiveFormsModule } from '@angular/forms';
import { By } from '@angular/platform-browser';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { COLOUR_PALETTE, ColourPickerComponent } from './colour-picker.component';

@Component({
  standalone: true,
  imports: [ColourPickerComponent, ReactiveFormsModule],
  template: `<app-colour-picker label="Colour" [formControl]="control" />`,
})
class HostComponent {
  readonly control = new FormControl(COLOUR_PALETTE[0], { nonNullable: true });
}

describe('ColourPickerComponent', () => {
  let fixture: ComponentFixture<ColourPickerComponent>;
  let component: ColourPickerComponent;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ColourPickerComponent, getTranslocoTestingModule()],
    }).compileComponents();

    fixture = TestBed.createComponent(ColourPickerComponent);
    fixture.componentRef.setInput('label', 'Colour');
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  function swatches(): HTMLButtonElement[] {
    return fixture.debugElement.queryAll(By.css('button.swatch')).map((d) => d.nativeElement as HTMLButtonElement);
  }

  function pickerInput(): HTMLInputElement {
    return fixture.debugElement.query(By.css('input[type=color]')).nativeElement as HTMLInputElement;
  }

  it('renders one swatch per palette colour plus the custom picker', () => {
    expect(swatches().length).toBe(COLOUR_PALETTE.length);
    expect(pickerInput()).toBeTruthy();
  });

  it('marks the swatch that matches the written value, ignoring case', () => {
    component.writeValue('#119E8F');
    fixture.detectChanges();

    const pressed = swatches().filter((b) => b.getAttribute('aria-pressed') === 'true');
    expect(pressed.map((b) => b.getAttribute('aria-label'))).toEqual(['#119e8f']);
    expect(component.customColour()).toBeNull();
  });

  it('emits the colour when a swatch is clicked', () => {
    const onChange = jasmine.createSpy('onChange');
    component.registerOnChange(onChange);

    swatches()[2].click();

    expect(onChange).toHaveBeenCalledWith(COLOUR_PALETTE[2]);
    expect(component.paletteMatch()).toBe(COLOUR_PALETTE[2]);
  });

  it('shows an out-of-palette value on the custom cell', () => {
    component.writeValue('#123456');
    fixture.detectChanges();

    expect(component.paletteMatch()).toBeNull();
    expect(component.customColour()).toBe('#123456');
    expect(fixture.debugElement.query(By.css('label.custom')).nativeElement.classList).toContain('on');
    expect(pickerInput().value).toBe('#123456');
  });

  it('emits any colour chosen in the native picker', () => {
    const onChange = jasmine.createSpy('onChange');
    component.registerOnChange(onChange);

    pickerInput().value = '#abcdef';
    pickerInput().dispatchEvent(new Event('input'));

    expect(onChange).toHaveBeenCalledWith('#abcdef');
    expect(component.customColour()).toBe('#abcdef');
  });

  it('selects a swatch when the picker is set to a palette colour', () => {
    pickerInput().value = COLOUR_PALETTE[4];
    pickerInput().dispatchEvent(new Event('input'));
    fixture.detectChanges();

    expect(component.paletteMatch()).toBe(COLOUR_PALETTE[4]);
    expect(component.customColour()).toBeNull();
  });

  it('disables every control when the form is disabled', () => {
    component.setDisabledState(true);
    fixture.detectChanges();

    expect(swatches().every((b) => b.disabled)).toBeTrue();
    expect(pickerInput().disabled).toBeTrue();
  });

  it('stays in sync with a reactive form control', () => {
    const host = TestBed.createComponent(HostComponent);
    host.detectChanges();

    host.componentInstance.control.setValue('#ef4444');
    host.detectChanges();
    const pressed = host.debugElement.queryAll(By.css('button.swatch[aria-pressed="true"]'));
    expect(pressed.map((d) => d.nativeElement.getAttribute('aria-label'))).toEqual(['#ef4444']);

    host.componentInstance.control.disable();
    host.detectChanges();
    expect(host.debugElement.query(By.css('input[type=color]')).nativeElement.disabled).toBeTrue();
  });

  it('uses a custom palette when one is given', () => {
    fixture.componentRef.setInput('palette', ['#000000', '#ffffff']);
    fixture.detectChanges();

    expect(swatches().map((b) => b.getAttribute('aria-label'))).toEqual(['#000000', '#ffffff']);
  });

  it('warns when a colour is too faint, only if contrast checking is on', () => {
    fixture.componentRef.setInput('checkContrast', true);
    component.writeValue('#f59e0b');
    fixture.detectChanges();

    expect(component.lowContrast()).toBeTrue();
    expect(fixture.debugElement.query(By.css('.contrast-warning'))).not.toBeNull();
  });

  it('shows the contrast hint for a readable colour when checking is on', () => {
    fixture.componentRef.setInput('checkContrast', true);
    component.writeValue('#6366f1');
    fixture.detectChanges();

    expect(component.lowContrast()).toBeFalse();
    expect(fixture.debugElement.query(By.css('.contrast-warning'))).toBeNull();
    expect(fixture.debugElement.query(By.css('p.hint'))).not.toBeNull();
  });

  it('does not check contrast unless asked to', () => {
    component.writeValue('#f59e0b');
    fixture.detectChanges();

    expect(component.lowContrast()).toBeFalse();
    expect(fixture.debugElement.query(By.css('.contrast-warning'))).toBeNull();
    expect(fixture.debugElement.query(By.css('p.hint'))).toBeNull();
  });
});
