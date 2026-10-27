import { ComponentFixture, TestBed } from "@angular/core/testing";
import { Router, provideRouter } from "@angular/router";
import { NEVER, Observable, of, throwError } from "rxjs";
import { ApiError } from "../../../core/api/api-client.service";
import { getTranslocoTestingModule } from "../../../core/transloco/transloco-testing.module";
import { WallDisplayComponent } from "./wall-display.component";
import { WallDisplayService } from "./wall-display.service";
import { WallSummary } from "./wall-summary.models";
import { WeatherResponse } from "../../weather/weather.models";

const summary: WallSummary = {
  family_name: "The Rivers",
  date: "2024-05-01",
  timezone: "UTC",
  now: "2024-05-01T09:00:00Z",
  options: {
    show_points: true,
    show_weather: false,
    show_tomorrow: false,
    show_pictures: false,
    show_greeting: true,
    night_mode: false,
    show_bonus_banner: false,
  },
  children: [
    {
      member_id: "m1",
      name: "Ava",
      colour: "#E07A5F",
      avatar_id: null,
      points: 12,
      tasks: [
        {
          occurrence_id: "o1",
          title: "Make bed",
          due_at: "2024-05-01T08:00:00Z",
          state: "done",
          completed_at: "2024-05-01T08:30:00Z",
        },
        {
          occurrence_id: "o2",
          title: "Read 20 minutes",
          due_at: "2024-05-01T18:00:00Z",
          state: "pending",
        },
      ],
    },
    {
      member_id: "m2",
      name: "Noah",
      colour: "#3D85C6",
      avatar_id: null,
      points: null,
      tasks: [
        {
          occurrence_id: "o3",
          title: "Feed the cat",
          due_at: "2024-05-01T07:00:00Z",
          state: "done",
          completed_at: "2024-05-01T07:10:00Z",
        },
      ],
    },
  ],
  events: [],
  bonus_tasks: [],
  weather: null,
};

describe("WallDisplayComponent", () => {
  let fixture: ComponentFixture<WallDisplayComponent>;
  let displaySpy: jasmine.SpyObj<WallDisplayService>;
  let router: Router;

  function kidSections(): HTMLElement[] {
    return Array.from(
      fixture.nativeElement.querySelectorAll('[data-testid="kid"]'),
    );
  }

  function create(source: Observable<WallSummary>): void {
    displaySpy.summary.and.returnValue(source);
    fixture = TestBed.createComponent(WallDisplayComponent);
    fixture.detectChanges();
  }

  beforeEach(() => {
    displaySpy = jasmine.createSpyObj<WallDisplayService>(
      "WallDisplayService",
      ["summary", "avatarUrl", "verifyExitCode", "unpair"],
    );
    displaySpy.avatarUrl.and.callFake(
      (id: string, size: 96 | 256) => `/api/v1/wall/avatars/${id}?size=${size}`,
    );
    TestBed.configureTestingModule({
      imports: [WallDisplayComponent, getTranslocoTestingModule()],
      providers: [
        { provide: WallDisplayService, useValue: displaySpy },
        provideRouter([]),
      ],
    });
    router = TestBed.inject(Router);
  });

  it("shows a waiting state until the first summary arrives", () => {
    create(NEVER);

    expect(
      fixture.nativeElement.querySelector('[data-testid="wall-waiting"]'),
    ).not.toBeNull();
    expect(kidSections().length).toBe(0);
  });

  it("renders one section per child with done rows", () => {
    create(of(summary));

    const sections = kidSections();
    expect(sections.length).toBe(2);
    expect(sections[0].querySelectorAll(".done .d:not(.more)").length).toBe(1);
  });

  it("keeps every child in the sideways row past three kids", () => {
    const extra = ["Mia", "Sam", "Jordan"].map((name, i) => ({
      member_id: `x${i}`,
      name,
      colour: "#81B29A",
      avatar_id: null,
      points: 3,
      tasks: [],
    }));
    create(of({ ...summary, children: [...summary.children, ...extra] }));

    const sections = kidSections();
    expect(sections.length).toBe(5);
    const row = sections[0].closest<HTMLElement>(".kids");
    expect(row).not.toBeNull();
    expect(getComputedStyle(row!).overflowX).toBe("auto");
    expect(sections.every((s) => row!.contains(s))).toBeTrue();
  });

  it("celebrates a child whose tasks are all done and not others", () => {
    create(of(summary));

    const sections = kidSections();
    expect(
      sections[0].querySelector('[data-testid="kid-celebrate"]'),
    ).toBeNull();
    expect(
      sections[1].querySelector('[data-testid="kid-celebrate"]'),
    ).not.toBeNull();
  });

  it("hides the greeting when the family turns it off", () => {
    create(
      of({ ...summary, options: { ...summary.options, show_greeting: false } }),
    );

    expect(
      fixture.nativeElement.querySelector('[data-testid="greet"]'),
    ).toBeNull();
  });

  it("shows offline and keeps the last view when the network is down", () => {
    create(throwError(() => ({ code: "network", status: 0 }) as ApiError));

    expect(
      fixture.nativeElement.querySelector('[data-testid="wall-waiting"]'),
    ).not.toBeNull();
    expect(
      fixture.nativeElement.querySelector('[data-testid="status"] i.off'),
    ).not.toBeNull();
  });

  it("sends an unpaired display back to the pair screen", () => {
    spyOn(router, "navigateByUrl").and.returnValue(Promise.resolve(true));

    create(
      throwError(() => ({ code: "unauthorized", status: 401 }) as ApiError),
    );

    expect(router.navigateByUrl).toHaveBeenCalledWith("/pair");
  });

  it("shows the bonus band with one card per bonus task when the family turns it on", () => {
    create(
      of({
        ...summary,
        options: { ...summary.options, show_bonus_banner: true },
        bonus_tasks: [
          {
            id: "b1",
            title: "Tidy the garage",
            points: 5,
            claim_mode: "first_come",
            ends_at: "2024-05-01T23:59:00Z",
          },
        ],
      }),
    );

    expect(
      fixture.nativeElement.querySelector('[data-testid="bonus"]'),
    ).not.toBeNull();
    expect(
      fixture.nativeElement.querySelectorAll('[data-testid="bonus-card"]')
        .length,
    ).toBe(1);
  });

  it("hides the bonus band when the family turns it off", () => {
    create(
      of({
        ...summary,
        bonus_tasks: [
          {
            id: "b1",
            title: "Tidy the garage",
            points: 5,
            claim_mode: "first_come",
            ends_at: "2024-05-01T23:59:00Z",
          },
        ],
      }),
    );

    expect(
      fixture.nativeElement.querySelector('[data-testid="bonus"]'),
    ).toBeNull();
  });

  describe("night dimming", () => {
    function createAt(iso: string): void {
      jasmine.clock().install();
      jasmine.clock().mockDate(new Date(iso));
      try {
        create(of(summary));
      } finally {
        jasmine.clock().uninstall();
      }
    }

    it("dims the kiosk from 21:00 in the family zone", () => {
      createAt("2024-05-01T22:00:00Z");

      expect(
        fixture.nativeElement.querySelector(".kiosk").classList.contains("dim"),
      ).toBeTrue();
    });

    it("keeps the kiosk bright during the day", () => {
      createAt("2024-05-01T12:00:00Z");

      expect(
        fixture.nativeElement.querySelector(".kiosk").classList.contains("dim"),
      ).toBeFalse();
    });
  });

  it("shows a birthday from date of birth with a cake and party emoji", () => {
    create(
      of({
        ...summary,
        events: [
          {
            title: "Ava",
            kind: "birthday",
            age: 11,
            date: "2024-05-01",
            starts_at: "2024-05-01T00:00:00Z",
            ends_at: "2024-05-02T00:00:00Z",
            all_day: true,
            location: null,
            attendees: [
              {
                member_id: "m1",
                name: "Ava",
                colour: "#E07A5F",
                avatar_id: null,
              },
            ],
          },
        ],
      }),
    );

    const rows = fixture.nativeElement.querySelectorAll(
      ".ev",
    ) as NodeListOf<HTMLElement>;
    expect(rows.length).toBe(1);
    expect(rows[0].textContent).toContain("🎂 Ava turns 11 🎉");
  });

  it("marks family-wide events as everyone and shows avatars for the children in others", () => {
    create(
      of({
        ...summary,
        events: [
          {
            title: "Family picnic",
            date: "2024-05-01",
            starts_at: "2024-05-01T12:00:00Z",
            ends_at: "2024-05-01T13:00:00Z",
            all_day: false,
            location: null,
            attendees: [],
          },
          {
            title: "Piano",
            date: "2024-05-01",
            starts_at: "2024-05-01T15:00:00Z",
            ends_at: "2024-05-01T16:00:00Z",
            all_day: false,
            location: null,
            attendees: [
              {
                member_id: "m1",
                name: "Ava",
                colour: "#E07A5F",
                avatar_id: null,
              },
            ],
          },
        ],
      }),
    );

    const rows = fixture.nativeElement.querySelectorAll(
      ".ev",
    ) as NodeListOf<HTMLElement>;
    expect(rows.length).toBe(2);
    expect(rows[0].querySelector(".who .all")).not.toBeNull();
    expect(rows[0].querySelector(".who .avatar")).toBeNull();
    expect(rows[1].querySelector(".who .all")).toBeNull();
    expect(rows[1].querySelector(".who .avatar")?.textContent?.trim()).toBe(
      "A",
    );
  });

  it("shows pictures where the wall has them and letters elsewhere", () => {
    create(
      of({
        ...summary,
        children: [
          { ...summary.children[0], avatar_id: "a1" },
          summary.children[1],
        ],
        events: [
          {
            title: "Piano",
            date: "2024-05-01",
            starts_at: "2024-05-01T15:00:00Z",
            ends_at: "2024-05-01T16:00:00Z",
            all_day: false,
            location: null,
            attendees: [
              { member_id: "m1", name: "Ava", colour: "#E07A5F", avatar_id: "a1" },
              { member_id: "m2", name: "Noah", colour: "#3D85C6", avatar_id: null },
            ],
          },
        ],
      }),
    );

    const kidPicture = kidSections()[0].querySelector<HTMLImageElement>(
      '[data-testid="kid-avatar"]',
    );
    expect(kidPicture?.getAttribute("src")).toBe(
      "/api/v1/wall/avatars/a1?size=256",
    );
    expect(
      kidSections()[1].querySelector('[data-testid="kid-avatar"]'),
    ).toBeNull();

    const attendees = fixture.nativeElement.querySelectorAll(
      ".ev .who .avatar",
    ) as NodeListOf<HTMLElement>;
    expect(attendees[0].tagName).toBe("IMG");
    expect(attendees[0].getAttribute("src")).toBe(
      "/api/v1/wall/avatars/a1?size=96",
    );
    expect(attendees[1].tagName).toBe("SPAN");
    expect(attendees[1].textContent?.trim()).toBe("N");
  });

  it("asks the browser for a location once when the family has none, then reloads with it", async () => {
    const locate = spyOn(navigator.geolocation, "getCurrentPosition").and.callFake(
      (ok: PositionCallback) =>
        ok({ coords: { latitude: 51.5, longitude: -0.12 } } as GeolocationPosition),
    );
    create(
      of({
        ...summary,
        options: { ...summary.options, show_weather: true },
        weather: { configured: false } as WeatherResponse,
      }),
    );

    await new Promise((resolve) => setTimeout(resolve));

    expect(locate).toHaveBeenCalledTimes(1);
    expect(displaySpy.summary).toHaveBeenCalledWith({ lat: 51.5, lon: -0.12 });
  });

  describe("exit keypad", () => {
    function hotspot(): HTMLButtonElement {
      return fixture.nativeElement.querySelector('[data-testid="exit-hot"]');
    }

    function keypad(): HTMLElement | null {
      return fixture.nativeElement.querySelector('[data-testid="exit-keypad"]');
    }

    function press(key: string): void {
      (
        fixture.nativeElement.querySelector(
          `[data-key="${key}"]`,
        ) as HTMLButtonElement
      ).click();
      fixture.detectChanges();
    }

    function typeCode(code: string): void {
      for (const digit of code) press(digit);
    }

    function checkButton(): HTMLButtonElement {
      return fixture.nativeElement.querySelector(
        '[data-key="\u2713"]',
      ) as HTMLButtonElement;
    }

    function lit(): number {
      return fixture.nativeElement.querySelectorAll(
        ".pin-dots i.on",
      ).length as number;
    }

    function message(): string {
      return (
        fixture.nativeElement.querySelector('[data-testid="exit-msg"]')
          ?.textContent ?? ""
      ).trim();
    }

    beforeEach(() => {
      displaySpy.verifyExitCode.and.returnValue(of(undefined));
      displaySpy.unpair.and.returnValue(of(undefined));
    });

    it("opens the keypad from the visible X", () => {
      create(of(summary));

      (
        fixture.nativeElement.querySelector(
          '[data-testid="exit-x"]',
        ) as HTMLButtonElement
      ).click();
      fixture.detectChanges();

      expect(keypad()).not.toBeNull();
      expect(displaySpy.verifyExitCode).not.toHaveBeenCalled();
    });

    it("opens the keypad only after the corner is held for 2.5 seconds", () => {
      jasmine.clock().install();
      try {
        create(of(summary));
        hotspot().dispatchEvent(new Event("pointerdown"));

        jasmine.clock().tick(2_400);
        fixture.detectChanges();
        expect(keypad()).toBeNull();

        jasmine.clock().tick(100);
        fixture.detectChanges();
        expect(keypad()).not.toBeNull();
      } finally {
        jasmine.clock().uninstall();
      }
    });

    it("cancels the hold when the finger lifts early", () => {
      jasmine.clock().install();
      try {
        create(of(summary));
        hotspot().dispatchEvent(new Event("pointerdown"));
        jasmine.clock().tick(1_000);
        hotspot().dispatchEvent(new Event("pointerup"));

        jasmine.clock().tick(5_000);
        fixture.detectChanges();
        expect(keypad()).toBeNull();
      } finally {
        jasmine.clock().uninstall();
      }
    });

    it("enables the check button from four digits and unlocks on a right code", () => {
      create(of(summary));
      (
        fixture.nativeElement.querySelector(
          '[data-testid="exit-x"]',
        ) as HTMLButtonElement
      ).click();
      fixture.detectChanges();

      typeCode("123");
      expect(checkButton().disabled).toBeTrue();

      typeCode("4");
      expect(checkButton().disabled).toBeFalse();
      expect(lit()).toBe(4);

      checkButton().click();
      fixture.detectChanges();

      expect(displaySpy.verifyExitCode).toHaveBeenCalledWith("1234");
      expect(
        fixture.nativeElement.querySelector('[data-testid="exit-unlocked"]'),
      ).not.toBeNull();
    });

    it("says how many tries are left after a wrong code and clears the digits", () => {
      displaySpy.verifyExitCode.and.returnValue(
        throwError(
          () =>
            ({
              code: "exit_code_wrong",
              status: 403,
              params: { attempts_left: 3 },
            }) as unknown as ApiError,
        ),
      );
      create(of(summary));
      (
        fixture.nativeElement.querySelector(
          '[data-testid="exit-x"]',
        ) as HTMLButtonElement
      ).click();
      fixture.detectChanges();

      typeCode("0000");
      checkButton().click();
      fixture.detectChanges();

      expect(message()).toBe("Wrong code. 3 tries left.");
      expect(lit()).toBe(0);
      expect(keypad()).not.toBeNull();
    });

    it("tells the parent how long the keypad is locked", () => {
      displaySpy.verifyExitCode.and.returnValue(
        throwError(
          () =>
            ({
              code: "exit_locked",
              status: 429,
              params: { retry_after: 300 },
            }) as unknown as ApiError,
        ),
      );
      create(of(summary));
      (
        fixture.nativeElement.querySelector(
          '[data-testid="exit-x"]',
        ) as HTMLButtonElement
      ).click();
      fixture.detectChanges();

      typeCode("1111");
      checkButton().click();
      fixture.detectChanges();

      expect(message()).toBe("Too many tries. Locked for 5 min.");
    });

    it("unpairs with the same code and sends the display to the pair screen", () => {
      spyOn(router, "navigateByUrl").and.returnValue(Promise.resolve(true));
      create(of(summary));
      (
        fixture.nativeElement.querySelector(
          '[data-testid="exit-x"]',
        ) as HTMLButtonElement
      ).click();
      fixture.detectChanges();
      typeCode("1234");
      checkButton().click();
      fixture.detectChanges();

      (
        fixture.nativeElement.querySelector(
          '[data-testid="exit-unpair"]',
        ) as HTMLButtonElement
      ).click();

      expect(displaySpy.unpair).toHaveBeenCalledWith("1234");
      expect(router.navigateByUrl).toHaveBeenCalledWith("/pair");
    });

    it("leaves wall mode to the sign-in page from the unlocked menu", () => {
      spyOn(router, "navigateByUrl").and.returnValue(Promise.resolve(true));
      create(of(summary));
      (
        fixture.nativeElement.querySelector(
          '[data-testid="exit-x"]',
        ) as HTMLButtonElement
      ).click();
      fixture.detectChanges();
      typeCode("1234");
      checkButton().click();
      fixture.detectChanges();

      (
        fixture.nativeElement.querySelector(
          '[data-testid="exit-wall"]',
        ) as HTMLButtonElement
      ).click();
      fixture.detectChanges();

      expect(router.navigateByUrl).toHaveBeenCalledWith("/login");
      expect(keypad()).toBeNull();
    });
  });

  it("shows a countdown on the next timed event, moving to tomorrow once today is over", () => {
    jasmine.clock().install();
    jasmine.clock().mockDate(new Date("2024-05-01T09:00:00Z"));
    try {
      create(
        of({
          ...summary,
          events: [
            {
              title: "Early swim",
              date: "2024-05-01",
              starts_at: "2024-05-01T07:00:00Z",
              ends_at: "2024-05-01T08:00:00Z",
              all_day: false,
              location: null,
              attendees: [],
            },
            {
              title: "Club",
              date: "2024-05-02",
              starts_at: "2024-05-02T08:00:00Z",
              ends_at: "2024-05-02T09:00:00Z",
              all_day: false,
              location: null,
              attendees: [],
            },
          ],
        }),
      );
    } finally {
      jasmine.clock().uninstall();
    }

    const counted = fixture.nativeElement.querySelectorAll(
      ".ev .in",
    ) as NodeListOf<HTMLElement>;
    expect(counted.length).toBe(1);
    expect(counted[0].closest(".ev")?.textContent).toContain("Club");
  });
});
