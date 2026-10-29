import type { MockedObject } from "vitest";
import { TestBed } from '@angular/core/testing';
import { of } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';
import { mockOf } from '../../testing/mock-of';
import { ForgotPasswordService } from './forgot-password.service';

describe('ForgotPasswordService', () => {
    let service: ForgotPasswordService;
    let apiSpy: MockedObject<ApiClient>;

    beforeEach(() => {
        apiSpy = mockOf<ApiClient>({
            post: vi.fn().mockName("ApiClient.post")
        });
        TestBed.configureTestingModule({ providers: [{ provide: ApiClient, useValue: apiSpy }] });
        service = TestBed.inject(ForgotPasswordService);
    });

    it('should post the token and new password to the reset endpoint', () => {
        apiSpy.post.mockReturnValue(of(undefined));
        service.resetPassword('tok-123', 'a long new password').subscribe();
        expect(apiSpy.post).toHaveBeenCalledWith('/api/v1/auth/reset-password', {
            token: 'tok-123',
            new_password: 'a long new password',
        });
    });

    it('should post the email to the email reset route', () => {
        apiSpy.post.mockReturnValue(of({ status: 'email_not_set_up' }));
        service.requestResetByEmail('sam@example.com').subscribe();
        expect(apiSpy.post).toHaveBeenCalledWith('/api/v1/auth/forgot-password/email', { email: 'sam@example.com' });
    });
});
