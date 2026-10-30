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

    function select(testId: string): HTMLSelectElement | null {
        return (fixture.debugElement.query(By.css(`[data-testid="${testId}"]`))?.nativeElement as HTMLSelectElement | undefined) ?? null;
    }

    function choose(testId: string, value: string): void {
        const el = select(testId)!;
        el.value = value;
        el.dispatchEvent(new Event('change'));
        fixture.detectChanges();
    }

    function optionValues(testId: string): string[] {
        return Array.from(select(testId)!.options).map((o) => o.value);
    }

    it('shows 12 hour options and an AM/PM menu in 12 hour mode', () => {
        expect(optionValues('time-hour')).toEqual(['', '01', '02', '03', '04', '05', '06', '07', '08', '09', '10', '11', '12']);
        expect(select('time-period')).toBeTruthy();
    });

    it('shows 24 hour options and no AM/PM menu in 24 hour mode', () => {
        twelveHour.set(false);
        fixture.detectChanges();

        expect(optionValues('time-hour').length).toBe(25);
        expect(optionValues('time-hour')).toContain('23');
        expect(select('time-period')).toBeNull();
    });

    it('displays a 24 hour value as 12 hour with the matching AM/PM', () => {
        component.writeValue('15:05');
        fixture.detectChanges();

        expect(select('time-hour')!.value).toBe('03');
        expect(select('time-minute')!.value).toBe('05');
        expect(select('time-period')!.value).toBe('PM');
    });

    it('treats 00:30 as 12 AM', () => {
        component.writeValue('00:30');
        fixture.detectChanges();

        expect(select('time-hour')!.value).toBe('12');
        expect(select('time-period')!.value).toBe('AM');
    });

    it('emits 24 hour HH:mm from a 12 hour choice', () => {
        choose('time-hour', '09');
        choose('time-minute', '30');
        choose('time-period', 'PM');

        expect(emitted.at(-1)).toBe('21:30');
    });

    it('fills minute 00 when only the hour is chosen', () => {
        choose('time-hour', '07');

        expect(select('time-minute')!.value).toBe('00');
        expect(emitted.at(-1)).toBe('07:00');
    });

    it('emits an empty string until both hour and minute are set', () => {
        choose('time-minute', '15');

        expect(emitted.at(-1)).toBe('');
    });

    it('keeps the 24 hour value unchanged in 24 hour mode', () => {
        twelveHour.set(false);
        fixture.detectChanges();

        choose('time-hour', '18');
        choose('time-minute', '45');

        expect(emitted.at(-1)).toBe('18:45');
    });

    it('clears the value when the hour is cleared', () => {
        component.writeValue('10:10');
        fixture.detectChanges();

        choose('time-hour', '');

        expect(emitted.at(-1)).toBe('');
    });
});
