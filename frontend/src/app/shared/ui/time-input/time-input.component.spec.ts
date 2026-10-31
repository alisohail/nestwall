import { signal, WritableSignal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { TimeFormatService } from '../../../core/time-format/time-format.service';
import { TimeInputComponent } from './time-input.component';

describe('TimeInputComponent', () => {
    let fixture: ComponentFixture<TimeInputComponent>;
    let component: TimeInputComponent;
    let twelveHour: WritableSignal<boolean>;
    let emitted: string[];

    beforeEach(async () => {
        twelveHour = signal(true);
        await TestBed.configureTestingModule({
            imports: [TimeInputComponent, getTranslocoTestingModule()],
            providers: [{ provide: TimeFormatService, useValue: { twelveHour } }],
        }).compileComponents();

        fixture = TestBed.createComponent(TimeInputComponent);
        fixture.componentRef.setInput('label', 'Start time');
        component = fixture.componentInstance;
        emitted = [];
        component.registerOnChange((value: string) => emitted.push(value));
        component.registerOnTouched(() => undefined);
        fixture.detectChanges();
    });

    function trigger(): HTMLInputElement {
        return fixture.debugElement.query(By.css('[data-testid="time-trigger"]')).nativeElement as HTMLInputElement;
    }

    function panel(): HTMLElement | null {
        return (fixture.debugElement.query(By.css('[data-testid="time-panel"]'))?.nativeElement as HTMLElement | undefined) ?? null;
    }

    function openPanel(): void {
        if (!panel()) {
            trigger().click();
            fixture.detectChanges();
        }
    }

    function tap(testId: string): void {
        openPanel();
        (fixture.debugElement.query(By.css(`[data-testid="${testId}"]`)).nativeElement as HTMLButtonElement).click();
        fixture.detectChanges();
    }

    function text(testId: string): string {
        return (fixture.debugElement.query(By.css(`[data-testid="${testId}"]`)).nativeElement as HTMLElement).textContent!.trim();
    }

    /** Types into the field and presses Enter, as a user would. */
    function typeTime(value: string): void {
        trigger().value = value;
        trigger().dispatchEvent(new Event('input'));
        trigger().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
        fixture.detectChanges();
    }

    it('shows the stepper and AM/PM toggle in 12 hour mode', () => {
        openPanel();

        expect(panel()!.querySelector('[data-testid="time-period-AM"]')).toBeTruthy();
        expect(text('time-hour-value')).toBe('--');
    });

    it('hides AM/PM in 24 hour mode', () => {
        twelveHour.set(false);
        fixture.detectChanges();
        openPanel();

        expect(panel()!.querySelector('[data-testid="time-period-AM"]')).toBeNull();
    });

    it('shows a 24 hour value as 12 hour with the matching AM/PM in 12 hour mode', () => {
        component.writeValue('15:05');
        fixture.detectChanges();

        expect(trigger().value).toMatch(/^03:05 /);
        openPanel();
        expect(text('time-hour-value')).toBe('03');
        expect(text('time-minute-value')).toBe('05');
    });

    it('shows a 24 hour value as HH:mm in 24 hour mode', () => {
        twelveHour.set(false);
        component.writeValue('15:05');
        fixture.detectChanges();

        expect(trigger().value).toBe('15:05');
    });

    it('treats 00:30 as 12 AM', () => {
        component.writeValue('00:30');
        fixture.detectChanges();

        expect(trigger().value).toMatch(/^12:30 /);
    });

    it('types 9:30 as 09:30 in 12 hour mode with AM by default', () => {
        typeTime('9:30');

        expect(emitted.at(-1)).toBe('09:30');
        expect(trigger().value).toMatch(/^09:30 /);
    });

    it('types 9:30 pm as 21:30', () => {
        typeTime('9:30 pm');

        expect(emitted.at(-1)).toBe('21:30');
    });

    it('accepts 24 hour input such as 21:30 in 12 hour mode', () => {
        typeTime('21:30');

        expect(emitted.at(-1)).toBe('21:30');
    });

    it('accepts 09:00 typed with a leading zero', () => {
        typeTime('09:00');

        expect(emitted.at(-1)).toBe('09:00');
    });

    it('drops text it cannot read and leaves the value alone', () => {
        typeTime('9:30');
        typeTime('banana');

        expect(emitted.at(-1)).toBe('09:30');
    });

    it('rejects minutes above 59', () => {
        typeTime('9:75');

        expect(emitted).toEqual([]);
    });

    it('commits typed text on blur', () => {
        trigger().value = '7:15 am';
        trigger().dispatchEvent(new Event('input'));
        trigger().dispatchEvent(new Event('blur'));

        expect(emitted.at(-1)).toBe('07:15');
    });

    it('clears the value when the text is emptied', () => {
        typeTime('10:10');
        typeTime('');

        expect(emitted.at(-1)).toBe('');
    });

    it('steps the hour up and down, wrapping inside the AM half', () => {
        component.writeValue('09:00');
        fixture.detectChanges();

        tap('time-hour-up');
        expect(emitted.at(-1)).toBe('10:00');

        tap('time-hour-down');
        tap('time-hour-down');
        expect(emitted.at(-1)).toBe('08:00');

        component.writeValue('11:00');
        fixture.detectChanges();
        tap('time-hour-up');
        expect(emitted.at(-1)).toBe('00:00');
    });

    it('steps the hour across 24 hours in 24 hour mode', () => {
        twelveHour.set(false);
        fixture.detectChanges();
        component.writeValue('23:00');
        fixture.detectChanges();

        tap('time-hour-up');

        expect(emitted.at(-1)).toBe('00:00');
    });

    it('steps minutes in 5 minute steps and carries into the hour', () => {
        component.writeValue('10:55');
        fixture.detectChanges();

        tap('time-minute-up');
        expect(emitted.at(-1)).toBe('11:00');

        tap('time-minute-down');
        expect(emitted.at(-1)).toBe('10:55');
    });

    it('snaps an off-step minute to the next step in the direction of the step', () => {
        component.writeValue('10:07');
        fixture.detectChanges();

        tap('time-minute-up');
        expect(emitted.at(-1)).toBe('10:10');

        component.writeValue('10:07');
        fixture.detectChanges();
        tap('time-minute-down');
        expect(emitted.at(-1)).toBe('10:05');
    });

    it('starts from 09:00 when the first step is pressed', () => {
        tap('time-minute-up');

        expect(emitted.at(-1)).toBe('09:05');
    });

    it('switches AM/PM and keeps the hour', () => {
        component.writeValue('09:30');
        fixture.detectChanges();

        tap('time-period-PM');

        expect(emitted.at(-1)).toBe('21:30');
    });

    it('closes the panel from Done and keeps the value', () => {
        component.writeValue('09:30');
        fixture.detectChanges();
        openPanel();

        tap('time-done');

        expect(panel()).toBeNull();
        expect(emitted.length).toBe(0);
    });

    it('clears the value from the panel', () => {
        component.writeValue('10:10');
        fixture.detectChanges();

        tap('time-clear');

        expect(emitted.at(-1)).toBe('');
        expect(trigger().value).toBe('');
        expect(panel()).toBeNull();
    });

    it('closes the panel on Escape and on a click outside', () => {
        openPanel();
        fixture.debugElement.nativeElement.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
        fixture.detectChanges();
        expect(panel()).toBeNull();

        openPanel();
        document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }));
        fixture.detectChanges();
        expect(panel()).toBeNull();
    });
});
