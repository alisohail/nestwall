import { provideHttpClient, withXhr } from "@angular/common/http";
import {
  HttpTestingController,
  provideHttpClientTesting,
} from "@angular/common/http/testing";
import { TestBed } from "@angular/core/testing";
import { TasksService } from "./tasks.service";

describe("TasksService", () => {
  let service: TasksService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(withXhr()), provideHttpClientTesting()],
    });
    service = TestBed.inject(TasksService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it("archives a task and sends its version as If-Match", () => {
    service.archive("t1", 3).subscribe();
    const req = http.expectOne("/api/v1/tasks/t1/archive");
    expect(req.request.method).toBe("POST");
    expect(req.request.headers.get("If-Match")).toBe('"3"');
    req.flush({});
  });

  it("removes one person's copy and sends the member and scope", () => {
    service.removePerson("t1", 3, "m1", "occurrence").subscribe();
    const req = http.expectOne("/api/v1/tasks/t1/remove-person");
    expect(req.request.method).toBe("POST");
    expect(req.request.headers.get("If-Match")).toBe('"3"');
    expect(req.request.body).toEqual({ member_id: "m1", scope: "occurrence" });
    req.flush({});
  });

  it("sends the due range for a list query", () => {
    service
      .list({
        status: "to_do",
        due_from: "2026-10-10",
        due_to: "2026-10-12",
      })
      .subscribe();
    const req = http.expectOne(
      "/api/v1/tasks?status=to_do&due_from=2026-10-10&due_to=2026-10-12",
    );
    expect(req.request.method).toBe("GET");
    req.flush({ tasks: [], next_cursor: null });
  });

  it("ticks a step on the named member's copy with the task version", () => {
    service.tickChecklist("t1", "i1", true, 4, "m2").subscribe();
    const req = http.expectOne("/api/v1/tasks/t1/checklist/i1");
    expect(req.request.method).toBe("PUT");
    expect(req.request.headers.get("If-Match")).toBe('"4"');
    expect(req.request.body).toEqual({ done: true, member_id: "m2" });
    req.flush({});
  });

  it("ticks a step on the signed-in copy when no member is named", () => {
    service.tickChecklist("t1", "i1", false, 4).subscribe();
    const req = http.expectOne("/api/v1/tasks/t1/checklist/i1");
    expect(req.request.body).toEqual({ done: false });
    req.flush({});
  });

  it("loads the named member's copy of a task", () => {
    service.get("t1", "m2").subscribe();
    const req = http.expectOne("/api/v1/tasks/t1?member_id=m2");
    expect(req.request.method).toBe("GET");
    req.flush({});
  });

  it("loads the default copy when no member is named", () => {
    service.get("t1").subscribe();
    const req = http.expectOne("/api/v1/tasks/t1");
    req.flush({});
  });

  it("lists archived tasks with the archived flag", () => {
    service.list({ archived: true }).subscribe();
    const req = http.expectOne("/api/v1/tasks?archived=true");
    expect(req.request.method).toBe("GET");
    req.flush({ tasks: [], next_cursor: null });
  });

  it("permanently deletes an archived task and sends its version as If-Match", () => {
    service.deleteTask("t1", 3).subscribe();
    const req = http.expectOne("/api/v1/tasks/t1");
    expect(req.request.method).toBe("DELETE");
    expect(req.request.headers.get("If-Match")).toBe('"3"');
    req.flush(null);
  });

  it("restores an archived task and sends its version as If-Match", () => {
    service.restore("t1", 4).subscribe();
    const req = http.expectOne("/api/v1/tasks/t1/unarchive");
    expect(req.request.method).toBe("POST");
    expect(req.request.headers.get("If-Match")).toBe('"4"');
    req.flush({});
  });
});
