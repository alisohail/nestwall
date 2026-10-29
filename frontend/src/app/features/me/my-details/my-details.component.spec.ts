import type { Mock } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { WritableSignal, signal } from '@angular/core';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { AuthService, MeResponse } from '../../../core/auth/auth.service';
import { MyDetailsComponent } from './my-details.component';

const parent: MeResponse = {
    user: {
        id: 'u1',
        user_id: 'sam',
        email: 'sam@example.com',
        display_name: 'Sam',
        is_child: false,
        must_change_password: false,
        date_of_birth: '1980-02-03',
        gender: 'male',
    },
    family: {
        member_id: 'm1',
        family_id: 'f1',
        family_name: 'Home',
        role_id: 'r-parent',
        role_name: 'Parent',
        status: 'active',
        colour: '#14b8a6',
        avatar_id: null,
        is_parent: true,
    },
};

const child: MeResponse = {
    user: {
        id: 'u2',
        user_id: 'ava',
        email: null,
        display_name: 'Ava',
        is_child: true,
        must_change_password: false,
        date_of_birth: null,
        gender: null,
    },
    family: { ...parent.family!, member_id: 'm2', role_id: 'r-child', role_name: 'Child', is_parent: false },
};

describe('MyDetailsComponent', () => {
    let me: WritableSignal<MeResponse | null | undefined>;
    let authSpy: {
        currentUser: WritableSignal<MeResponse | null | undefined>;
        updateProfile: Mock;
    };

    beforeEach(async () => {
        me = signal<MeResponse | null | undefined>(parent);
        authSpy = {
            currentUser: me,
            updateProfile: vi.fn().mockName('updateProfile').mockReturnValue(of(undefined)),
        };

        await TestBed.configureTestingModule({
            imports: [MyDetailsComponent, getTranslocoTestingModule()],
            providers: [{ provide: AuthService, useValue: authSpy }],
        }).compileComponents();
    });

    function create(user: MeResponse): ComponentFixture<MyDetailsComponent> {
        me.set(user);
        const fixture = TestBed.createComponent(MyDetailsComponent);
        fixture.detectChanges();
        return fixture;
    }

    it('should fill the form from the signed-in user', () => {
        const fixture = create(parent);
        expect(fixture.componentInstance.form.getRawValue()).toEqual({
            display_name: 'Sam',
            email: 'sam@example.com',
            date_of_birth: '1980-02-03',
            gender: 'male',
        });
    });

    it('should require an email address for a parent', () => {
        const fixture = create(parent);
        const email = fixture.componentInstance.form.controls.email;
        email.setValue('');
        expect(email.hasError('required')).toBe(true);
    });

    it('should allow a child to leave the email empty', () => {
        const fixture = create(child);
        const email = fixture.componentInstance.form.controls.email;
        email.setValue('');
        expect(fixture.componentInstance.isParent).toBe(false);
        expect(email.valid).toBe(true);
    });

    it('should save trimmed and lowercased values and show the saved message', () => {
        const fixture = create(parent);
        const component = fixture.componentInstance;
        component.form.setValue({ display_name: '  Sam Lee ', email: 'SAM@Example.com', date_of_birth: '1980-02-03', gender: 'male' });

        component.save();
        fixture.detectChanges();

        expect(authSpy.updateProfile).toHaveBeenCalledWith({
            display_name: 'Sam Lee',
            email: 'sam@example.com',
            date_of_birth: '1980-02-03',
            gender: 'male',
        });
        expect(fixture.nativeElement.querySelector('[data-testid="my-details-saved"]')).toBeTruthy();
    });

    it('should not call the API while the form is invalid', () => {
        const fixture = create(parent);
        fixture.componentInstance.form.controls.display_name.setValue('   ');

        fixture.componentInstance.save();

        expect(authSpy.updateProfile).not.toHaveBeenCalled();
    });

    it('should show the email taken message when another member has the address', () => {
        authSpy.updateProfile.mockReturnValue(throwError(() => ({ status: 409, code: 'email_taken' })));
        const fixture = create(parent);

        fixture.componentInstance.save();
        fixture.detectChanges();

        expect(fixture.componentInstance.error()).toBe('family.edit.error.email_taken');
        expect(fixture.nativeElement.querySelector('[data-testid="my-details-error"]')).toBeTruthy();
    });
});
