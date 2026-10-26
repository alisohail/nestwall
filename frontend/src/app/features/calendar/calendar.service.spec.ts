import { TestBed } from '@angular/core/testing';
import { of } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';
import { CalendarService } from './calendar.service';
import { EventRequest, OccurrenceRequest } from './calendar.models';

const body: EventRequest = {
  title: 'Swim',
  description: null,
  location: null,
  all_day: false,
  time_zone: 'UTC',
  start_date: '2026-10-09',
  start_time: '16:00',
  end_date: '2026-10-09',
  end_time: '17:00',
  recurrence_rule: null,
  member_ids: ['m1'],
  reminders: [15],
};

describe('CalendarService event writes', () => {
  let api: jasmine.SpyObj<ApiClient>;
  let service: CalendarService;

  beforeEach(() => {
    api = jasmine.createSpyObj<ApiClient>('ApiClient', ['get', 'post', 'put', 'download']);
    api.post.and.returnValue(of({} as never));
    api.put.and.returnValue(of({} as never));
    api.get.and.returnValue(of({} as never));
    api.download.and.returnValue(of(new Blob()));
    TestBed.configureTestingModule({ providers: [{ provide: ApiClient, useValue: api }] });
    service = TestBed.inject(CalendarService);
  });

  it('creates an event with a POST to the events collection', () => {
    service.createEvent(body).subscribe();

    expect(api.post).toHaveBeenCalledWith('/api/v1/calendar/events', body);
  });

  it('loads one event by id', () => {
    service.getEvent('ev1').subscribe();

    expect(api.get).toHaveBeenCalledWith('/api/v1/calendar/events/ev1');
  });

  it('downloads the family export as a file, not JSON', () => {
    service.exportFamily().subscribe();

    expect(api.download).toHaveBeenCalledWith('/api/v1/calendar/export.ics');
  });

  it('updates the whole series with the loaded version as If-Match', () => {
    service.updateEvent('ev1', body, 3).subscribe();

    expect(api.put).toHaveBeenCalledWith('/api/v1/calendar/events/ev1', body, 3);
  });

  it('changes one occurrence by its original date, with If-Match', () => {
    const occurrence: OccurrenceRequest = {
      title: 'Swim (moved)',
      description: null,
      location: null,
      start_date: '2026-10-16',
      start_time: '17:00',
      end_date: '2026-10-16',
      end_time: '18:00',
    };

    service.editOccurrence('ev1', '2026-10-09', occurrence, 4).subscribe();

    expect(api.put).toHaveBeenCalledWith('/api/v1/calendar/events/ev1/occurrences/2026-10-09', occurrence, 4);
  });
});
