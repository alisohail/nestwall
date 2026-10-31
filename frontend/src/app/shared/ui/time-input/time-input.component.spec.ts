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

    function trigger(): HTMLButtonElement {
        return fixture.debugElement.query(By.css('[data-testid="time-trigger"]')).nativeElement as HTMLButtonElement;
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

    function cellLabels(prefix: string): string[] {
        openPanel();
        return fixture.debugElement
            .queryAll(By.css(`[data-testid^="${prefix}"]`))
            .map((d) => (d.nativeElement as HTMLElement).textContent!.trim());
    }

    function isSelected(testId: string): boolean {
        openPanel();
        return (fixture.debugElement.query(By.css(`[data-testid="${testId}"]`)).nativeElement as HTMLElement).classList.contains('selected');
    }

    function shown(): string {
        return Array.from(trigger().querySelectorAll('.time-value, .time-period-label, .time-placeholder'))
            .map((node) => node.textContent!.trim())
            .join(' ');
    }

    it('shows 12 hour cells and an AM/PM toggle in 12 hour mode', () => {
        expect(cellLabels('time-hour-')).toEqual(['01', '02', '03', '04', '05', '06', '07', '08', '09', '10', '11', '12']);
        expect(panel()!.querySelector('[data-testid="time-period-AM"]')).toBeTruthy();
    });

    it('shows 24 hour cells and no AM/PM toggle in 24 hour mode', () => {
        twelveHour.set(false);
        fixture.detectChanges();

        expect(cellLabels('time-hour-').length).toBe(24);
        expect(cellLabels('time-hour-')).toContain('23');
        expect(panel()!.querySelector('[data-testid="time-period-AM"]')).toBeNull();
    });

    it('offers 5 minute steps', () => {
        const minutes = cellLabels('time-minute-');

        expect(minutes.length).toBe(12);
        expect(minutes[1]).toBe('05');
        expect(minutes.at(-1)).toBe('55');
    });

    it('shows a 24 hour value as 12 hour with the matching AM/PM', () => {
        component.writeValue('15:05');
        fixture.detectChanges();

        expect(shown()).toBe('03:05 PM');
        expect(isSelected('time-hour-03')).toBe(true);
        expect(isSelected('time-minute-05')).toBe(true);
        expect(isSelected('time-period-PM')).toBe(true);
    });

    it('treats 00:30 as 12 AM', () => {
        component.writeValue('00:30');
        fixture.detectChanges();

        expect(shown()).toBe('12:30 AM');
        expect(isSelected('time-hour-12')).toBe(true);
    });

    it('keeps a minute that is off the 5 minute steps selected', () => {
        component.writeValue('10:07');
        fixture.detectChanges();

        expect(cellLabels('time-minute-')).toContain('07');
        expect(isSelected('time-minute-07')).toBe(true);
    });

    it('emits 24 hour HH:mm from a 12 hour choice', () => {
        tap('time-hour-09');
        tap('time-minute-30');
        tap('time-period-PM');
        tap('time-minute-30');

        expect(emitted.at(-1)).toBe('21:30');
    });

    it('fills minute 00 when only the hour is chosen', () => {
        tap('time-hour-07');

        expect(shown()).toBe('07:00 AM');
        expect(emitted.at(-1)).toBe('07:00');
    });

    it('closes the panel once a minute is chosen', () => {
        tap('time-hour-07');
        expect(panel()).not.toBeNull();

        tap('time-minute-15');
        expect(panel()).toBeNull();
    });

    it('emits an empty string until both hour and minute are set', () => {
        tap('time-minute-15');

        expect(emitted.at(-1)).toBe('');
    });

    it('keeps the 24 hour value unchanged in 24 hour mode', () => {
        twelveHour.set(false);
        fixture.detectChanges();

        tap('time-hour-18');
        tap('time-minute-45');

        expect(emitted.at(-1)).toBe('18:45');
    });

    it('clears the value from the panel', () => {
        component.writeValue('10:10');
        fixture.detectChanges();

        tap('time-clear');

        expect(emitted.at(-1)).toBe('');
        expect(trigger().textContent!.trim()).toBe('Choose time');
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
