import type { MockedObject } from "vitest";
import { TestBed } from '@angular/core/testing';
import { of } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';
import { CalendarService } from './calendar.service';
import { mockOf } from '../../testing/mock-of';

describe('CalendarService imports', () => {
    let api: MockedObject<ApiClient>;
    let service: CalendarService;

    beforeEach(() => {
        api = mockOf<ApiClient>({
            get: vi.fn().mockName("ApiClient.get"),
            post: vi.fn().mockName("ApiClient.post"),
            delete: vi.fn().mockName("ApiClient.delete")
        });
        api.get.mockReturnValue(of({ items: [] } as never));
        api.post.mockReturnValue(of({} as never));
        api.delete.mockReturnValue(of(undefined as never));
        TestBed.configureTestingModule({ providers: [{ provide: ApiClient, useValue: api }] });
        service = TestBed.inject(CalendarService);
    });

    it('lists imports with a GET to the imports collection', () => {
        service.listImports().subscribe();

        expect(api.get).toHaveBeenCalledWith('/api/v1/calendar/imports');
    });

    it('creates an import by posting the form as multipart', () => {
        const form = new FormData();
        form.append('name', 'School');
        form.append('audience', 'specific');
        form.append('member_ids', 'm1');

        service.createImport(form).subscribe();

        expect(api.post).toHaveBeenCalledWith('/api/v1/calendar/imports', form);
    });

    it('deletes an import by id', () => {
        service.deleteImport('imp1').subscribe();

        expect(api.delete).toHaveBeenCalledWith('/api/v1/calendar/imports/imp1');
    });
});
