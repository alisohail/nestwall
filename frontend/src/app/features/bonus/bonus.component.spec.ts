import { provideHttpClient } from "@angular/common/http";
import { provideHttpClientTesting } from "@angular/common/http/testing";
import { ComponentFixture, TestBed } from "@angular/core/testing";
import { provideRouter } from "@angular/router";
import { NEVER, of, throwError } from "rxjs";
import { getTranslocoTestingModule } from "../../core/transloco/transloco-testing.module";
import { FamilyService } from "../family/family.service";
import { BonusClaim, BonusTask } from "./bonus.models";
import { BonusService } from "./bonus.service";
import { BonusComponent } from "./bonus.component";

type ListReturn = ReturnType<BonusService["list"]>;
type ApproveReturn = ReturnType<BonusService["approve"]>;
type EditReturn = ReturnType<BonusService["edit"]>;
type CancelReturn = ReturnType<BonusService["cancel"]>;
type UnassignReturn = ReturnType<BonusService["unassign"]>;

const NOW = "2026-10-13T09:00:00Z";

function claim(over: Partial<BonusClaim> = {}): BonusClaim {
  return {
    id: "c1",
    bonus_task_id: "t1",
    family_member_id: "m1",
    status: "submitted",
    claimed_at: NOW,
    released_at: null,
    completed_at: NOW,
    decided_at: null,
    decided_by_member_id: null,
    decision_note: null,
    version: 3,
    ...over,
  };
}

function task(over: Partial<BonusTask> = {}): BonusTask {
  return {
    id: "t1",
    template_id: null,
    title: "Wash the car",
    description: null,
    points: 15,
    claim_mode: "first_come",
    approval_required: true,
    show_from: null,
    ends_at: "2026-10-13T23:59:59Z",
    day: "2026-10-13",
    state: "open",
    cancelled_at: null,
    version: 1,
    editable: true,
    eligible_member_ids: ["m1", "m2"],
    claims: [],
    ...over,
  };
}

describe("BonusComponent", () => {
  let fixture: ComponentFixture<BonusComponent>;
  let api: jasmine.SpyObj<BonusService>;

  function build(): void {
    fixture = TestBed.createComponent(BonusComponent);
    fixture.detectChanges();
  }

  function el<T extends Element = HTMLElement>(selector: string): T | null {
    return fixture.nativeElement.querySelector(selector) as T | null;
  }

  function click(selector: string): void {
    el<HTMLElement>(selector)?.click();
    fixture.detectChanges();
  }

  function fill(selector: string, value: string): void {
    const input = el<HTMLInputElement | HTMLTextAreaElement>(selector);
    if (input) {
      input.value = value;
      input.dispatchEvent(new Event("input"));
    }
    fixture.detectChanges();
  }

  beforeEach(async () => {
    api = jasmine.createSpyObj<BonusService>("BonusService", [
      "list",
      "create",
      "approve",
      "reject",
      "edit",
      "cancel",
      "unassign",
    ]);
    api.list.and.returnValue(of({ tasks: [] }) as unknown as ListReturn);
    api.approve.and.returnValue(of({}) as unknown as ApproveReturn);
    api.reject.and.returnValue(of({}) as unknown as ApproveReturn);
    api.edit.and.returnValue(of(task()) as unknown as EditReturn);
    api.cancel.and.returnValue(
      of(task({ state: "cancelled" })) as unknown as CancelReturn,
    );
    api.unassign.and.returnValue(
      of(claim({ status: "released" })) as unknown as UnassignReturn,
    );

    await TestBed.configureTestingModule({
      imports: [BonusComponent, getTranslocoTestingModule()],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: BonusService, useValue: api },
        {
          provide: FamilyService,
          useValue: {
            listMembers: () =>
              of({
                members: [
                  {
                    id: "m1",
                    display_name: "Noah",
                    colour: "#2a9d8f",
                    avatar_id: null,
                    is_child: true,
                    status: "active",
                  },
                  {
                    id: "m2",
                    display_name: "Mia",
                    colour: "#e76f51",
                    avatar_id: null,
                    is_child: true,
                    status: "active",
                  },
                ],
              }),
          } as unknown as FamilyService,
        },
      ],
    }).compileComponents();
  });

  it("should show the loading state until the tasks arrive", () => {
    api.list.and.returnValue(NEVER as unknown as ListReturn);
    build();
    expect(el('[data-testid="bonus-loading"]')).toBeTruthy();
    expect(el('[data-testid="bonus-empty"]')).toBeNull();
  });

  it("should show the empty state when there are no tasks today", () => {
    build();
    expect(el('[data-testid="bonus-empty"]')).toBeTruthy();
  });

  it("should show the error state and retry on failure", () => {
    api.list.and.returnValue(
      throwError(() => ({
        status: 500,
        code: "internal",
      })) as unknown as ListReturn,
    );
    build();
    expect(el('[data-testid="bonus-load-error"]')).toBeTruthy();
    api.list.and.returnValue(of({ tasks: [] }) as unknown as ListReturn);
    click('[data-testid="bonus-load-error"] button');
    expect(el('[data-testid="bonus-empty"]')).toBeTruthy();
  });

  it("should show the first_come holder on the card", () => {
    api.list.and.returnValue(
      of({
        tasks: [
          task({ claims: [claim({ status: "claimed", completed_at: null })] }),
        ],
      }) as unknown as ListReturn,
    );
    build();
    expect(el('[data-testid="bonus-card-t1"]')).toBeTruthy();
    expect(el('[data-testid="bonus-row-t1-m1"]')?.textContent).toContain(
      "Noah",
    );
    expect(el('[data-testid="bonus-nobody"]')).toBeNull();
  });

  it("should show the day and the window on each bonus row", () => {
    api.list.and.returnValue(
      of({
        tasks: [
          task({
            show_from: new Date(2026, 9, 13, 16, 0).toISOString(),
            ends_at: new Date(2026, 9, 13, 21, 0).toISOString(),
          }),
        ],
      }) as unknown as ListReturn,
    );
    build();
    const when = el('[data-testid="bonus-when"]')?.textContent ?? "";
    expect(when).toContain(fixture.componentInstance.dayLabel("2026-10-13"));
    expect(when).toContain(
      fixture.componentInstance.time(
        new Date(2026, 9, 13, 16, 0).toISOString(),
      ),
    );
  });

  it("should show nobody yet for a first_come task with no live claim", () => {
    api.list.and.returnValue(of({ tasks: [task()] }) as unknown as ListReturn);
    build();
    expect(el('[data-testid="bonus-nobody"]')).toBeTruthy();
  });

  it("should show a row for every eligible kid on everyone_once tasks", () => {
    api.list.and.returnValue(
      of({
        tasks: [task({ claim_mode: "everyone_once", claims: [claim()] })],
      }) as unknown as ListReturn,
    );
    build();
    expect(el('[data-testid="bonus-row-t1-m1"]')).toBeTruthy();
    expect(el('[data-testid="bonus-row-t1-m2"]')).toBeTruthy();
  });

  it("should approve a submitted claim with its version", () => {
    api.list.and.returnValue(
      of({ tasks: [task({ claims: [claim()] })] }) as unknown as ListReturn,
    );
    build();
    click('[data-testid="bonus-approve"]');
    expect(api.approve).toHaveBeenCalledWith("c1", 3);
    expect(el('[data-testid="bonus-notice"]')).toBeTruthy();
  });

  it("should reload and show the changed message when the claim is stale", () => {
    api.list.and.returnValue(
      of({ tasks: [task({ claims: [claim()] })] }) as unknown as ListReturn,
    );
    api.approve.and.returnValue(
      throwError(() => ({
        status: 412,
        code: "precondition_failed",
      })) as unknown as ApproveReturn,
    );
    build();
    const before = api.list.calls.count();
    click('[data-testid="bonus-approve"]');
    expect(el('[data-testid="bonus-error"]')).toBeTruthy();
    expect(api.list.calls.count()).toBeGreaterThan(before);
  });

  it("should send a claim back with the note", async () => {
    api.list.and.returnValue(
      of({ tasks: [task({ claims: [claim()] })] }) as unknown as ListReturn,
    );
    build();
    click('[data-testid="bonus-send-back"]');
    await fixture.whenStable();
    fixture.detectChanges();
    const note = el<HTMLTextAreaElement>(
      '[data-testid="bonus-send-back-note"]',
    );
    expect(note).toBeTruthy();
    if (note) {
      note.value = "Try the back seat too";
      note.dispatchEvent(new Event("input"));
    }
    fixture.detectChanges();
    click('[data-testid="bonus-send-back-confirm"]');
    expect(api.reject).toHaveBeenCalledWith("c1", 3, "Try the back seat too");
  });

  it("should load the scheduled tab and show its rows", () => {
    api.list.and.returnValue(
      of({
        tasks: [
          task({
            id: "t2",
            title: "Tidy room",
            state: "scheduled",
            day: "2026-10-14",
            show_from: "2026-10-14T08:00:00Z",
            ends_at: "2026-10-14T20:00:00Z",
          }),
        ],
      }) as unknown as ListReturn,
    );
    build();
    click('[data-testid="bonus-tab-scheduled"]');
    expect(api.list).toHaveBeenCalledWith("scheduled");
    expect(el('[data-testid="bonus-scheduled-row-t2"]')?.textContent).toContain(
      "Tidy room",
    );
    expect(el('[data-testid="bonus-card-t2"]')).toBeNull();
  });

  it("should show the approved kids as the result on the past tab", () => {
    api.list.and.returnValue(
      of({
        tasks: [
          task({
            id: "t3",
            state: "expired",
            claims: [
              claim({ id: "c3", family_member_id: "m2", status: "approved" }),
            ],
          }),
        ],
      }) as unknown as ListReturn,
    );
    build();
    click('[data-testid="bonus-tab-past"]');
    expect(api.list).toHaveBeenCalledWith("past");
    expect(el('[data-testid="bonus-past-row-t3"]')?.textContent).toContain(
      "Done by Mia",
    );
  });

  it("should show an empty message on a tab with no tasks", () => {
    build();
    click('[data-testid="bonus-tab-scheduled"]');
    expect(el('[data-testid="bonus-empty-scope"]')).toBeTruthy();
    expect(el('[data-testid="bonus-empty"]')).toBeNull();
  });

  it("should label a task that ends at midnight as the end of the day", () => {
    build();
    const midnight = new Date(2026, 9, 14, 0, 0).toISOString();
    const evening = new Date(2026, 9, 13, 20, 0).toISOString();
    expect(
      fixture.componentInstance.untilLabel(task({ ends_at: midnight })),
    ).toEqual({
      key: "kidBonus.untilEndOfDay",
      params: {},
    });
    expect(
      fixture.componentInstance.untilLabel(task({ ends_at: evening })).key,
    ).toBe("bonus.until");
  });

  it("should unassign a grabbed task so it reopens", () => {
    api.list.and.returnValue(
      of({
        tasks: [
          task({
            editable: false,
            claims: [claim({ status: "claimed", completed_at: null })],
          }),
        ],
      }) as unknown as ListReturn,
    );
    build();
    click('[data-testid="bonus-unassign"]');
    expect(api.unassign).toHaveBeenCalledWith("c1", 3);
    expect(el('[data-testid="bonus-notice"]')).toBeTruthy();
  });

  it("should save only the fields that changed", async () => {
    api.list.and.returnValue(of({ tasks: [task()] }) as unknown as ListReturn);
    build();
    click('[data-testid="bonus-edit-t1"]');
    await fixture.whenStable();
    fixture.detectChanges();
    fill('[data-testid="bonus-edit-title"]', "Wash the van");
    click('[data-testid="bonus-edit-save"]');
    expect(api.edit).toHaveBeenCalledWith("t1", 1, { title: "Wash the van" });
  });

  it("should refuse a points value outside the range without calling the API", async () => {
    api.list.and.returnValue(of({ tasks: [task()] }) as unknown as ListReturn);
    build();
    click('[data-testid="bonus-edit-t1"]');
    await fixture.whenStable();
    fixture.detectChanges();
    fill('[data-testid="bonus-edit-points"]', "0");
    click('[data-testid="bonus-edit-save"]');
    expect(api.edit).not.toHaveBeenCalled();
    expect(el('[data-testid="bonus-edit-error"]')).toBeTruthy();
  });

  it("should cancel a task only after the confirmation", () => {
    api.list.and.returnValue(of({ tasks: [task()] }) as unknown as ListReturn);
    build();
    click('[data-testid="bonus-cancel-t1"]');
    expect(el('[data-testid="bonus-edit-panel"]')).toBeTruthy();
    expect(api.cancel).not.toHaveBeenCalled();
    click('[data-testid="bonus-cancel-confirm"]');
    expect(api.cancel).toHaveBeenCalledWith("t1", 1);
  });

  it("should delete a scheduled task only after the confirmation", () => {
    api.list.and.returnValue(
      of({
        tasks: [task({ id: "t3", state: "scheduled" })],
      }) as unknown as ListReturn,
    );
    build();
    click('[data-testid="bonus-tab-scheduled"]');
    click('[data-testid="bonus-scheduled-delete-t3"]');
    expect(el('[data-testid="bonus-edit-panel"]')).toBeTruthy();
    expect(api.cancel).not.toHaveBeenCalled();
    click('[data-testid="bonus-cancel-confirm"]');
    expect(api.cancel).toHaveBeenCalledWith("t3", 1);
  });

  it("should explain a refused edit when someone has already taken the task", async () => {
    api.list.and.returnValue(of({ tasks: [task()] }) as unknown as ListReturn);
    api.edit.and.returnValue(
      throwError(() => ({
        status: 409,
        code: "has_claims",
      })) as unknown as EditReturn,
    );
    build();
    const before = api.list.calls.count();
    click('[data-testid="bonus-edit-t1"]');
    await fixture.whenStable();
    fixture.detectChanges();
    fill('[data-testid="bonus-edit-title"]', "Wash the van");
    click('[data-testid="bonus-edit-save"]');
    expect(el('[data-testid="bonus-error"]')?.textContent).toContain(
      "Someone has already taken this task",
    );
    expect(el('[data-testid="bonus-edit-panel"]')).toBeNull();
    expect(api.list.calls.count()).toBeGreaterThan(before);
  });
});
