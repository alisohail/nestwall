import { CHILD_NAV, PARENT_MOBILE_NAV, PARENT_NAV, splitMobileNav } from './shell.component';

describe('shell navigation lists', () => {
    it('lists parent destinations in sidebar order', () => {
        expect(PARENT_NAV.map((item) => item.path)).toEqual([
            '/dashboard',
            '/tasks',
            '/bonus',
            '/approvals',
            '/calendar',
            '/rewards',
            '/points',
            '/family',
            '/settings',
        ]);
    });

    it('uses Home as the parent bottom-bar label for the dashboard only', () => {
        expect(PARENT_MOBILE_NAV[0].labelKey).toBe('shell.nav.home');
        expect(PARENT_MOBILE_NAV.slice(1)).toEqual(PARENT_NAV.slice(1));
    });

    it('gives children exactly four tabs in order, with no Me tab', () => {
        expect(CHILD_NAV.map((item) => item.path)).toEqual(['/today', '/my-tasks', '/calendar', '/rewards']);
    });
});

describe('splitMobileNav', () => {
    const labels = (items: {
        labelKey: string;
    }[]) => items.map((item) => item.labelKey);

    it('shows every parent destination in sidebar order when they fit', () => {
        const { visible, overflow } = splitMobileNav(PARENT_MOBILE_NAV, 820);
        expect(visible.length).toBe(PARENT_MOBILE_NAV.length);
        expect(overflow).toEqual([]);
        expect(labels(visible)[0]).toBe('shell.nav.home');
        expect(labels(visible).slice(-1)).toEqual(['shell.nav.settings']);
    });

    it('shows all nine exactly at the fit width of 648px', () => {
        expect(splitMobileNav(PARENT_MOBILE_NAV, 648).overflow).toEqual([]);
    });

    it('keeps one slot for More on a phone and moves the rest under it in order', () => {
        // 390px gives 5 slots: four destinations plus More.
        const { visible, overflow } = splitMobileNav(PARENT_MOBILE_NAV, 390);
        expect(labels(visible)).toEqual(['shell.nav.home', 'shell.nav.tasks', 'shell.nav.bonus', 'shell.nav.approvals']);
        expect(labels(overflow)).toEqual([
            'shell.nav.calendar',
            'shell.nav.rewards',
            'shell.nav.points',
            'shell.nav.family',
            'shell.nav.settings',
        ]);
    });

    it('returns no destinations for widths too narrow for any slot', () => {
        const { visible, overflow } = splitMobileNav(PARENT_MOBILE_NAV, 40);
        expect(visible).toEqual([]);
        expect(overflow.length).toBe(PARENT_MOBILE_NAV.length);
    });

    it('keeps all items and adds no More when the list fits', () => {
        const { visible, overflow } = splitMobileNav(PARENT_MOBILE_NAV.slice(0, 3), 216);
        expect(visible.length).toBe(3);
        expect(overflow).toEqual([]);
    });
});
