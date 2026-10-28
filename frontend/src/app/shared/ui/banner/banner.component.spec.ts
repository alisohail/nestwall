import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { BannerComponent } from './banner.component';

@Component({
    imports: [BannerComponent],
    template: `<app-banner [tone]="tone">Saved</app-banner>`
})
class HostComponent {
  tone: 'info' | 'success' | 'warning' | 'error' = 'info';
}

describe('BannerComponent', () => {
  let fixture: ComponentFixture<HostComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({ imports: [HostComponent] }).compileComponents();
    fixture = TestBed.createComponent(HostComponent);
    fixture.detectChanges();
  });

  it('projects its message body', () => {
    expect(fixture.nativeElement.textContent).toContain('Saved');
  });

  it('uses status role for info', () => {
    const el = fixture.debugElement.query(By.css('.nw-banner')).nativeElement as HTMLElement;
    expect(el.getAttribute('role')).toBe('status');
    expect(el.classList).toContain('nw-banner--info');
  });

  it('uses alert role for error', () => {
    fixture.componentInstance.tone = 'error';
    fixture.detectChanges();
    const el = fixture.debugElement.query(By.css('.nw-banner')).nativeElement as HTMLElement;
    expect(el.getAttribute('role')).toBe('alert');
    expect(el.classList).toContain('nw-banner--error');
  });

  it('marks the icon as decorative', () => {
    const icon = fixture.debugElement.query(By.css('.nw-banner__icon')).nativeElement as HTMLElement;
    expect(icon.getAttribute('aria-hidden')).toBe('true');
  });

  it('uses an icon that matches the tone by default', () => {
    const icon = () => (fixture.debugElement.query(By.css('.nw-banner__icon')).nativeElement as HTMLElement).textContent?.trim();
    expect(icon()).toBe('info');
    fixture.componentInstance.tone = 'error';
    fixture.detectChanges();
    expect(icon()).toBe('error');
  });
});
