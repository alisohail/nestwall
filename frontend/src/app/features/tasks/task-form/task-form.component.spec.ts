import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { of } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { FamilyService } from '../../family/family.service';
import { TasksService } from '../tasks.service';
import { TaskFormComponent } from './task-form.component';

describe('TaskFormComponent points', () => {
  let fixture: ComponentFixture<TaskFormComponent>;
  let component: TaskFormComponent;
  let create: jasmine.Spy;

  beforeEach(async () => {
    create = jasmine.createSpy('create').and.returnValue(of({ id: 't1' }));
    const tasks = {
      categories: () => of({ categories: [] }),
      templates: () => of({ templates: [], next_cursor: null }),
      create,
    } as unknown as TasksService;

    await TestBed.configureTestingModule({
      imports: [TaskFormComponent, getTranslocoTestingModule()],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: TasksService, useValue: tasks },
        { provide: FamilyService, useValue: { listMembers: () => of({ members: [] }) } as unknown as FamilyService },
      ],
    }).compileComponents();

    spyOn(TestBed.inject(Router), 'navigateByUrl').and.returnValue(Promise.resolve(true));
    fixture = TestBed.createComponent(TaskFormComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
  });

  function pointsInput(): HTMLInputElement {
    return fixture.nativeElement.querySelector('input[formcontrolname="points"]') as HTMLInputElement;
  }

  it('starts at 0 instead of a placeholder', () => {
    expect(component.f.points.value).toBe(0);
    expect(pointsInput().value).toBe('0');
    expect(pointsInput().placeholder).toBe('');
  });

  it('rejects an empty points value as required', () => {
    component.f.points.setValue(null);
    expect(component.f.points.hasError('required')).toBeTrue();
  });

  it('rejects points above the maximum', () => {
    component.f.points.setValue(1001);
    expect(component.f.points.hasError('max')).toBeTrue();
  });

  it('accepts 0 as a valid points value', () => {
    component.f.points.setValue(0);
    expect(component.f.points.valid).toBeTrue();
  });

  it('blocks save and shows the field error when points are cleared', () => {
    component.f.title.setValue('Make bed');
    component.f.assignees.setValue(['k1']);
    component.f.points.setValue(null);
    component.save();
    fixture.detectChanges();
    expect(create).not.toHaveBeenCalled();
    expect(fixture.nativeElement.querySelector('input[formcontrolname="points"]').getAttribute('aria-invalid')).toBe('true');
  });

  it('sends 0 points when the value is 0', () => {
    component.f.title.setValue('Make bed');
    component.f.assignees.setValue(['k1']);
    component.save();
    expect(create).toHaveBeenCalledWith(jasmine.objectContaining({ points: 0 }));
  });
});
