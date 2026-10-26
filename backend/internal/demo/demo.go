// Package demo creates the Rivera demo family that shows the parent and child
// screens with realistic data. Only the seed-demo command calls it; the API never does.
//
// The family is created in one transaction and marked in its settings, so a
// second run changes nothing. Recurring occurrences come from the same engine the
// scheduler uses. Completion and approval states are written directly, because the
// action handlers need a signed-in user.
package demo

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/badges/catalogue"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/rewards"
	"nestwall/backend/internal/tasks"
	"nestwall/backend/internal/tasks/seed"
)

const (
	// FamilyName is the name of the demo family.
	FamilyName = "Rivera family"
	// TimeZone is the demo family's IANA time zone.
	TimeZone = "America/Toronto"

	// marker identifies the demo family in families.settings.
	marker = "rivera-v1"

	roleParent      = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	roleChild       = "c3d4e5f6-a7b8-9012-cdef-123456789012"
	roleGrandparent = "5f2e8a10-7c3d-4b9e-9a61-2d4c8e0f1a37"

	dateLayout   = "2006-01-02"
	clockLayout  = "15:04"
	dbTimeLayout = "2006-01-02 15:04:05.000"

	stateToDo      = "to_do"
	stateSubmitted = "submitted"
	stateApproved  = "approved"
)

// ErrFamilyExists means the database already holds a family that is not the demo
// family. The demo is never added next to a real family.
var ErrFamilyExists = errors.New("the database already holds a family that is not the demo family")

// Result says what Seed did. Password is set only when Created is true. The same
// password is used for every demo account.
type Result struct {
	Created  bool
	Password string
}

type person struct {
	login   string
	name    string
	roleID  string
	colour  string
	isChild bool
}

var people = []person{
	{login: "sam", name: "Sam", roleID: roleParent, colour: "#6366F1"},
	{login: "jordan", name: "Jordan", roleID: roleParent, colour: "#0EA5E9"},
	{login: "ava", name: "Ava", roleID: roleChild, colour: "#14B8A6", isChild: true},
	{login: "noah", name: "Noah", roleID: roleChild, colour: "#F59E0B", isChild: true},
	{login: "mia", name: "Mia", roleID: roleChild, colour: "#EC4899", isChild: true},
	{login: "nana", name: "Nana", roleID: roleGrandparent, colour: "#8B5CF6"},
}

type templateSpec struct {
	name      string
	category  string
	partOfDay string
	dueTime   string
	rule      string
	points    int
	approval  bool
}

var templateSpecs = []templateSpec{
	{name: "Make bed", category: "chores", partOfDay: "anytime", rule: "FREQ=DAILY", points: 3, approval: true},
	{name: "Feed the dog", category: "pets", partOfDay: "morning", dueTime: "08:00", rule: "FREQ=DAILY", points: 5, approval: true},
	{name: "Tidy room", category: "chores", partOfDay: "anytime", dueTime: "17:00", points: 10, approval: true},
	{name: "Set the table", category: "chores", partOfDay: "evening", dueTime: "18:00", points: 5, approval: true},
	{name: "Homework", category: "school_work", partOfDay: "after_school", dueTime: "15:30", points: 15, approval: true},
	{name: "Take out recycling", category: "chores", partOfDay: "evening", dueTime: "17:00", points: 5, approval: true},
}

// timedState is one person's completion of a recurring task, at a local clock time.
type timedState struct {
	login string
	clock string
}

// repeatSpec is a recurring task. The people in done are assigned to it, and they
// complete it yesterday and today.
type repeatSpec struct {
	template     string
	expiresAfter *int
	done         []timedState
}

var repeats = []repeatSpec{
	{
		template:     "Make bed",
		expiresAfter: intPtr(2880),
		done: []timedState{
			{login: "ava", clock: "07:20"},
			{login: "noah", clock: "07:45"},
			{login: "mia", clock: "07:30"},
		},
	},
	{
		template: "Feed the dog",
		done:     []timedState{{login: "ava", clock: "07:35"}},
	},
}

type penaltyRule struct {
	value int
	unit  string
}

// oneOff is a single task. day is the local day offset from today. completed is the
// local completion time of today's task and is used only for submitted or approved states.
type oneOff struct {
	title         string
	category      string
	partOfDay     string
	dueTime       string
	day           int
	points        int
	approval      bool
	assignee      string
	state         string
	completed     string
	expiresIn     time.Duration
	penalty       *penaltyRule
	checklist     []string
	checklistDone int
}

var oneOffs = []oneOff{
	// Ava
	{title: "Homework", category: "school_work", partOfDay: "after_school", dueTime: "15:30", points: 15, approval: true, assignee: "ava", state: stateSubmitted, completed: "08:12", checklist: []string{"Read chapter 4", "Maths sheet", "Spelling list"}, checklistDone: 3},
	{title: "Tidy room", category: "chores", partOfDay: "anytime", dueTime: "17:00", points: 10, approval: true, assignee: "ava", state: stateToDo, checklist: []string{"Pick up toys", "Put clothes away", "Vacuum the rug"}, checklistDone: 1},
	{title: "Set the table", category: "chores", partOfDay: "evening", dueTime: "18:00", points: 5, approval: true, assignee: "ava", state: stateToDo, checklist: []string{"Plates", "Cups", "Cutlery"}, checklistDone: 1},
	{title: "Practise piano", category: "activities", partOfDay: "after_school", dueTime: "18:00", day: -1, points: 5, approval: true, assignee: "ava", state: stateToDo, expiresIn: 31 * time.Hour},
	{title: "Return library book", category: "school_work", partOfDay: "after_school", dueTime: "16:00", day: 1, points: 5, approval: true, assignee: "ava", state: stateToDo, checklist: []string{"Find the book", "Put it in the bag"}},
	{title: "Clean the bathroom", category: "chores", partOfDay: "morning", dueTime: "09:00", day: 2, points: 10, approval: true, assignee: "ava", state: stateToDo, checklist: []string{"Wipe the sink", "Clean the mirror", "Empty the bin"}},
	// Noah
	{title: "Feed the fish", category: "pets", partOfDay: "morning", dueTime: "08:00", points: 5, approval: true, assignee: "noah", state: stateApproved, completed: "07:50"},
	{title: "Maths worksheet", category: "school_work", partOfDay: "after_school", dueTime: "18:00", points: 15, approval: true, assignee: "noah", state: stateToDo},
	{title: "Set the table", category: "chores", partOfDay: "evening", dueTime: "18:00", points: 5, approval: true, assignee: "noah", state: stateToDo},
	{title: "Shower", category: "self_care", partOfDay: "evening", dueTime: "19:30", points: 3, approval: false, assignee: "noah", state: stateToDo},
	{title: "Pack school bag", category: "school_work", partOfDay: "evening", dueTime: "20:00", points: 3, approval: true, assignee: "noah", state: stateToDo},
	{title: "Reading, 20 min", category: "school_work", partOfDay: "after_school", dueTime: "19:00", day: -1, points: 10, approval: true, assignee: "noah", state: stateToDo, expiresIn: 32 * time.Hour, penalty: &penaltyRule{value: 5, unit: "points"}},
	{title: "Take out recycling", category: "chores", partOfDay: "evening", dueTime: "17:00", day: -1, points: 5, approval: true, assignee: "noah", state: stateToDo, expiresIn: 6*time.Hour + 18*time.Minute, penalty: &penaltyRule{value: 30, unit: "percent"}},
	// Mia
	{title: "Water the plants", category: "outdoor", partOfDay: "morning", dueTime: "08:00", points: 8, approval: true, assignee: "mia", state: stateApproved, completed: "08:05"},
	{title: "Brush teeth", category: "self_care", partOfDay: "morning", dueTime: "08:30", points: 3, approval: false, assignee: "mia", state: stateApproved, completed: "08:15"},
	{title: "Pack school bag", category: "school_work", partOfDay: "morning", dueTime: "08:30", points: 3, approval: true, assignee: "mia", state: stateApproved, completed: "08:20"},
}

// rewardSpec is one catalogue item, matching the mock's rewards page.
type rewardSpec struct {
	key         string
	name        string
	icon        string
	cost        int
	limitPeriod string
	limitCount  int
	approval    bool
	weekdays    int // bit 0 is Monday; zero means every day
}

// weekendBits is Saturday and Sunday in the weekday mask.
const weekendBits = 1<<5 | 1<<6

var rewardSpecs = []rewardSpec{
	{key: "dessert", name: "Pick dessert", icon: "🍦", cost: 40, limitPeriod: "week", limitCount: 2, approval: true},
	{key: "late", name: "Stay up 30 min late", icon: "🛏️", cost: 80, weekdays: weekendBits},
	{key: "movie", name: "Movie night pick", icon: "🎬", cost: 120, limitPeriod: "week", limitCount: 1},
	{key: "screen", name: "30 min screen time", icon: "📱", cost: 60, approval: true, weekdays: weekendBits},
	{key: "outing", name: "Family outing", icon: "🎡", cost: 500, approval: true},
}

// pastAward is a completion from an earlier day. It is posted to the ledger with no task
// reference, because the demo does not keep those occurrences.
type pastAward struct {
	daysAgo int
	title   string
	points  int
}

var pastAwards = map[string][]pastAward{
	"ava": {
		{2, "Water the plants", 8}, {2, "Make bed", 3}, {3, "Make bed", 3}, {3, "Tidy room", 10},
		{3, "Set the table", 5}, {4, "Make bed", 3}, {4, "Feed the dog", 5}, {5, "Make bed", 3},
		{5, "Homework", 15}, {6, "Make bed", 3}, {6, "Feed the dog", 5}, {7, "Tidy room", 10},
		{8, "Homework", 15}, {9, "Tidy room", 10}, {10, "Make bed", 3}, {11, "Feed the dog", 5},
		{12, "Homework", 15}, {13, "Tidy room", 10}, {14, "Set the table", 5}, {15, "Homework", 15},
		{16, "Tidy room", 10}, {17, "Make bed", 3}, {18, "Feed the dog", 5}, {19, "Homework", 15},
		{20, "Water the plants", 8},
	},
	"noah": {
		{2, "Make bed", 3}, {2, "Feed the fish", 5}, {3, "Reading, 20 min", 10}, {4, "Set the table", 5},
		{5, "Maths worksheet", 15}, {6, "Pack school bag", 3}, {7, "Feed the fish", 5},
		{8, "Reading, 20 min", 10}, {9, "Maths worksheet", 15}, {10, "Take out recycling", 5},
	},
	"mia": {
		{2, "Water the plants", 8}, {3, "Brush teeth", 3}, {4, "Pack school bag", 3}, {5, "Water the plants", 8},
		{6, "Brush teeth", 3}, {7, "Water the plants", 8}, {8, "Pack school bag", 3},
		{9, "Water the plants", 8}, {10, "Brush teeth", 3},
	},
}

// redemptionSpec is one reward request. A pending or approved request posts a negative
// redemption entry. A rejected request also posts a reversal, as the rewards package does.
type redemptionSpec struct {
	login   string
	reward  string
	status  string
	daysAgo int
	reason  string // rejections only
}

var redemptionSpecs = []redemptionSpec{
	{login: "ava", reward: "screen", status: "approved", daysAgo: 2},
	{login: "ava", reward: "dessert", status: "pending"},
	{login: "mia", reward: "dessert", status: "rejected", daysAgo: 1, reason: "Dinner is already planned"},
}

type memberRef struct {
	userID   string
	memberID string
}

// definition holds the columns for one task definition.
type definition struct {
	templateID   *string
	title        string
	categoryID   string
	partOfDay    string
	dueTime      string
	rule         string
	startsOn     string
	points       int
	approval     bool
	expiresAfter *int
	penalty      *penaltyRule
}

type seeder struct {
	ctx        context.Context
	tx         *sql.Tx
	familyID   string
	zone       *time.Location
	now        time.Time // the clock instant, in UTC
	today      time.Time // midnight of the family's local date
	hash       string
	members    map[string]memberRef
	categories map[string]string
	templates  map[string]string
	rewardIDs  map[string]string
}

// Seed creates the Rivera demo family with a random password. See SeedWithPassword.
func Seed(ctx context.Context, conn *sql.DB, clk clock.Clock) (Result, error) {
	return SeedWithPassword(ctx, conn, clk, "")
}

// SeedWithPassword creates the Rivera demo family in an empty database. An empty
// password means a random one is generated. If the demo family already exists, it
// returns a Result with Created set to false and changes nothing. If another family
// exists, it returns ErrFamilyExists.
func SeedWithPassword(ctx context.Context, conn *sql.DB, clk clock.Clock, password string) (Result, error) {
	if password == "" {
		generated, err := newPassword()
		if err != nil {
			return Result{}, err
		}
		password = generated
	} else if err := auth.ValidatePassword(password); err != nil {
		return Result{}, fmt.Errorf("demo: password: %w", err)
	}

	zone, err := time.LoadLocation(TimeZone)
	if err != nil {
		return Result{}, fmt.Errorf("demo: load time zone: %w", err)
	}
	now := clk.Now().UTC()
	local := now.In(zone)

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()

	settings, found, err := familySettings(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	if found {
		if isDemo(settings) {
			return Result{}, nil
		}
		return Result{}, ErrFamilyExists
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return Result{}, fmt.Errorf("demo: hash password: %w", err)
	}

	s := &seeder{
		ctx:        ctx,
		tx:         tx,
		zone:       zone,
		now:        now,
		today:      time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone),
		hash:       hash,
		members:    make(map[string]memberRef, len(people)),
		categories: make(map[string]string),
		templates:  make(map[string]string, len(templateSpecs)),
		rewardIDs:  make(map[string]string, len(rewardSpecs)),
	}
	if err := s.build(); err != nil {
		return Result{}, fmt.Errorf("demo: seed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{}, err
	}
	return Result{Created: true, Password: password}, nil
}

func (s *seeder) build() error {
	if err := s.insertFamily(); err != nil {
		return err
	}
	if err := seed.SeedDefaultCategories(s.ctx, s.tx, s.familyID); err != nil {
		return fmt.Errorf("default categories: %w", err)
	}
	if err := s.addGrandparentRole(); err != nil {
		return fmt.Errorf("grandparent role: %w", err)
	}
	if err := catalogue.SeedDefaults(s.ctx, s.tx, s.familyID); err != nil {
		return fmt.Errorf("default badges: %w", err)
	}
	if err := s.loadCategories(); err != nil {
		return err
	}
	for _, p := range people {
		if err := s.addPerson(p); err != nil {
			return fmt.Errorf("person %s: %w", p.login, err)
		}
	}
	if err := audit.Record(s.ctx, s.tx, s.familyID, s.sam().userID, "demo.seeded", "family", &s.familyID, map[string]any{"marker": marker}); err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	for _, t := range templateSpecs {
		if err := s.addTemplate(t); err != nil {
			return fmt.Errorf("template %s: %w", t.name, err)
		}
	}
	if err := s.addBonusTemplates(); err != nil {
		return fmt.Errorf("bonus templates: %w", err)
	}
	for _, r := range repeats {
		if err := s.addRepeat(r); err != nil {
			return fmt.Errorf("repeat %s: %w", r.template, err)
		}
	}
	for _, o := range oneOffs {
		if err := s.addOneOff(o); err != nil {
			return fmt.Errorf("task %s: %w", o.title, err)
		}
	}
	if err := s.addRewards(); err != nil {
		return fmt.Errorf("rewards: %w", err)
	}
	if err := s.addPastAwards(); err != nil {
		return fmt.Errorf("past awards: %w", err)
	}
	if err := s.addBonus(); err != nil {
		return fmt.Errorf("bonus: %w", err)
	}
	if err := s.addBonusTasks(); err != nil {
		return fmt.Errorf("bonus tasks: %w", err)
	}
	if err := s.addRedemptions(); err != nil {
		return fmt.Errorf("redemptions: %w", err)
	}
	// Align statuses with the clock at run time, using the same rules as the scheduler.
	if _, err := tasks.MarkPastDue(s.ctx, s.tx, s.now); err != nil {
		return fmt.Errorf("mark past due: %w", err)
	}
	if _, _, err := tasks.MarkMissed(s.ctx, s.tx, s.now); err != nil {
		return fmt.Errorf("mark missed: %w", err)
	}
	return nil
}

func (s *seeder) sam() memberRef {
	return s.members["sam"]
}

func (s *seeder) insertFamily() error {
	s.familyID = ids.New()
	settings, err := json.Marshal(map[string]string{"locale": "en", "demo_seed": marker})
	if err != nil {
		return err
	}
	_, err = s.tx.ExecContext(s.ctx,
		`INSERT INTO families (id, name, timezone, settings) VALUES (?, ?, ?, ?)`,
		s.familyID, FamilyName, TimeZone, string(settings))
	return err
}

func (s *seeder) loadCategories() error {
	rows, err := s.tx.QueryContext(s.ctx,
		`SELECT seed_key, id FROM task_categories WHERE family_id = ?`, s.familyID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key, id string
		if err := rows.Scan(&key, &id); err != nil {
			return err
		}
		s.categories[key] = id
	}
	return rows.Err()
}

// grandparentGrants are the grandparent role's permissions. It can see the family's tasks,
// calendar, members, family and rewards, and manage its own profile and sessions.
var grandparentGrants = []struct{ code, level string }{
	{"tasks.view", "view"},
	{"calendar.view", "view"},
	{"members.view", "view"},
	{"family.view", "view"},
	{"rewards.view", "view"},
	{"profile.manage", "allowed"},
	{"profile.avatar.change", "allowed"},
	{"settings.theme", "allowed"},
	{"sessions.manage", "allowed"},
}

// addGrandparentRole creates the demo's custom role. Its grants go into both the current
// grants and the defaults, so Reset to defaults restores them.
func (s *seeder) addGrandparentRole() error {
	if _, err := s.tx.ExecContext(s.ctx,
		`INSERT INTO roles (id, name, description, is_fixed) VALUES (?, 'Grandparent', 'Read-only view of the family calendar, tasks and rewards.', 0)`,
		roleGrandparent); err != nil {
		return err
	}
	for _, g := range grandparentGrants {
		for _, table := range []string{"role_permissions", "role_default_permissions"} {
			if _, err := s.tx.ExecContext(s.ctx,
				`INSERT INTO `+table+` (role_id, permission_id, access_level)
				 SELECT ?, id, ? FROM permissions WHERE code = ?`,
				roleGrandparent, g.level, g.code); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *seeder) addPerson(p person) error {
	userID := ids.New()
	if _, err := s.tx.ExecContext(s.ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, ?, ?, ?)`,
		userID, p.login, s.hash, p.name, boolInt(p.isChild)); err != nil {
		return err
	}
	memberID := ids.New()
	if _, err := s.tx.ExecContext(s.ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour) VALUES (?, ?, ?, ?, 'active', ?)`,
		memberID, s.familyID, userID, p.roleID, p.colour); err != nil {
		return err
	}
	s.members[p.login] = memberRef{userID: userID, memberID: memberID}
	return nil
}

func (s *seeder) addTemplate(t templateSpec) error {
	id := ids.New()
	if _, err := s.tx.ExecContext(s.ctx,
		`INSERT INTO task_templates (id, family_id, kind, name, title, default_points, priority, approval_required, category_id, part_of_day, due_time, recurrence_rule, created_by)
		 VALUES (?, ?, 'regular', ?, ?, ?, 'normal', ?, ?, ?, ?, ?, ?)`,
		id, s.familyID, t.name, t.name, t.points, boolInt(t.approval),
		s.categories[t.category], t.partOfDay, nilIfEmpty(t.dueTime), nilIfEmpty(t.rule), s.sam().userID); err != nil {
		return err
	}
	s.templates[t.name] = id
	return nil
}

// bonusTemplateSpec is one saved bonus template. The mock's bonus-new chips and settings list these
// five, with the same points.
type bonusTemplateSpec struct {
	name        string
	category    string
	description string
	points      int
}

var bonusTemplateSpecs = []bonusTemplateSpec{
	{name: "Wash the car", category: "chores", description: "Hand wash and vacuum inside", points: 20},
	{name: "Fold the laundry", category: "chores", points: 10},
	{name: "Water the garden", category: "outdoor", points: 8},
	{name: "Clean the garage", category: "chores", points: 25},
	{name: "Clean the bathroom", category: "chores", points: 15},
}

// addBonusTemplates seeds the bonus templates. A bonus template never carries a penalty, so none is set.
func (s *seeder) addBonusTemplates() error {
	for _, t := range bonusTemplateSpecs {
		if _, err := s.tx.ExecContext(s.ctx,
			`INSERT INTO task_templates (id, family_id, kind, name, title, description, default_points, priority, approval_required, category_id, created_by)
			 VALUES (?, ?, 'bonus', ?, ?, ?, ?, 'normal', 1, ?, ?)`,
			ids.New(), s.familyID, t.name, t.name, nilIfEmpty(t.description), t.points,
			s.categories[t.category], s.sam().userID); err != nil {
			return fmt.Errorf("%s: %w", t.name, err)
		}
	}
	return nil
}

func (s *seeder) addRepeat(r repeatSpec) error {
	tpl, err := findTemplate(r.template)
	if err != nil {
		return err
	}
	templateID := s.templates[tpl.name]
	defID, err := s.insertDefinition(definition{
		templateID:   &templateID,
		title:        tpl.name,
		categoryID:   s.categories[tpl.category],
		partOfDay:    tpl.partOfDay,
		dueTime:      tpl.dueTime,
		rule:         tpl.rule,
		startsOn:     s.day(-1),
		points:       tpl.points,
		approval:     tpl.approval,
		expiresAfter: r.expiresAfter,
	}, s.sam().userID)
	if err != nil {
		return err
	}
	for _, d := range r.done {
		if err := s.addDefinitionAssignee(defID, d.login); err != nil {
			return err
		}
	}
	// Generates the rolling window from today, with the same rules the scheduler uses.
	if _, err := tasks.GenerateWindow(s.ctx, s.tx, s.familyID, defID, s.now, tasks.DefaultWindowDays); err != nil {
		return fmt.Errorf("generate window: %w", err)
	}

	// Yesterday's occurrence is written directly because the window starts today.
	yesterday, err := s.insertOccurrence(defID, -1, tpl.dueTime, nil)
	if err != nil {
		return err
	}
	today, err := s.occurrenceOn(defID, 0)
	if err != nil {
		return err
	}
	for _, d := range r.done {
		if err := s.assign(yesterday, d.login); err != nil {
			return err
		}
		if err := s.applyState(yesterday, d.login, stateApproved, -1, d.clock); err != nil {
			return err
		}
		if err := s.award(yesterday, d.login, tpl.points, -1, d.clock); err != nil {
			return err
		}
		if err := s.applyState(today, d.login, stateApproved, 0, d.clock); err != nil {
			return err
		}
		if err := s.award(today, d.login, tpl.points, 0, d.clock); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) addOneOff(o oneOff) error {
	defID, err := s.insertDefinition(definition{
		title:      o.title,
		categoryID: s.categories[o.category],
		partOfDay:  o.partOfDay,
		dueTime:    o.dueTime,
		startsOn:   s.day(o.day),
		points:     o.points,
		approval:   o.approval,
		penalty:    o.penalty,
	}, s.sam().userID)
	if err != nil {
		return err
	}
	if err := s.addDefinitionAssignee(defID, o.assignee); err != nil {
		return err
	}
	var expires *time.Time
	if o.expiresIn > 0 {
		e := s.now.Add(o.expiresIn)
		expires = &e
	}
	occ, err := s.insertOccurrence(defID, o.day, o.dueTime, expires)
	if err != nil {
		return err
	}
	if err := s.assign(occ, o.assignee); err != nil {
		return err
	}
	if err := s.applyState(occ, o.assignee, o.state, 0, o.completed); err != nil {
		return err
	}
	if o.state == stateApproved {
		if err := s.award(occ, o.assignee, o.points, 0, o.completed); err != nil {
			return err
		}
	}
	for i, text := range o.checklist {
		id := ids.New()
		if _, err := s.tx.ExecContext(s.ctx,
			`INSERT INTO task_checklist_items (id, family_id, task_occurrence_id, sort_order, item_text)
			 VALUES (?, ?, ?, ?, ?)`,
			id, s.familyID, occ, i+1, text); err != nil {
			return err
		}
		if i < o.checklistDone {
			if _, err := s.tx.ExecContext(s.ctx,
				`INSERT INTO task_checklist_completions (family_id, task_occurrence_assignee_id, task_checklist_item_id, completed_at, completed_by_member_id)
				 SELECT oa.family_id, oa.id, ?, ?, ? FROM task_occurrence_assignees oa
				 WHERE oa.family_id = ? AND oa.task_occurrence_id = ? AND oa.family_member_id = ?`,
				id, stamp(s.clamp(s.now)), s.members[o.assignee].memberID,
				s.familyID, occ, s.members[o.assignee].memberID); err != nil {
				return err
			}
		}
	}
	return nil
}

// award posts the points for one approved task occurrence to the ledger. The reference
// is the occurrence, so the ledger links back to the task as the scheduler does.
func (s *seeder) award(occ, login string, points, offset int, clock string) error {
	when, err := s.at(offset, clock)
	if err != nil {
		return err
	}
	member := s.members[login].memberID
	_, err = rewards.Post(s.ctx, s.tx, s.clamp(when), rewards.Entry{
		FamilyID:       s.familyID,
		MemberID:       member,
		Type:           rewards.TaskAward,
		Amount:         points,
		ReferenceType:  "task_occurrence",
		ReferenceID:    occ,
		IdempotencyKey: "task_award:" + occ + ":" + member,
	})
	return err
}

func (s *seeder) addRewards() error {
	for _, r := range rewardSpecs {
		var limitPeriod, limitCount, days any
		if r.limitPeriod != "" {
			limitPeriod = r.limitPeriod
			limitCount = r.limitCount
		}
		if r.weekdays != 0 {
			days = r.weekdays
		}
		id := ids.New()
		if _, err := s.tx.ExecContext(s.ctx,
			`INSERT INTO rewards (id, family_id, name, icon, cost, status, limit_period, limit_count, approval_required, available_days)
			 VALUES (?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
			id, s.familyID, r.name, r.icon, r.cost, limitPeriod, limitCount, boolInt(r.approval), days); err != nil {
			return err
		}
		s.rewardIDs[r.key] = id
	}
	return nil
}

// addPastAwards posts earlier points that have no task occurrence in the demo, so the
// balances reflect a family that has been using the app for a few weeks.
func (s *seeder) addPastAwards() error {
	for _, login := range []string{"ava", "noah", "mia"} {
		member := s.members[login].memberID
		for i, a := range pastAwards[login] {
			when, err := s.at(-a.daysAgo, "12:00")
			if err != nil {
				return err
			}
			if _, err := rewards.Post(s.ctx, s.tx, s.clamp(when), rewards.Entry{
				FamilyID:       s.familyID,
				MemberID:       member,
				Type:           rewards.TaskAward,
				Amount:         a.points,
				Reason:         a.title,
				IdempotencyKey: fmt.Sprintf("seed:past:%s:%d", login, i),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// addBonus posts one parent bonus, so the ledger shows a non-task entry.
func (s *seeder) addBonus() error {
	when, err := s.at(-1, "18:00")
	if err != nil {
		return err
	}
	_, err = rewards.Post(s.ctx, s.tx, s.clamp(when), rewards.Entry{
		FamilyID:       s.familyID,
		MemberID:       s.members["ava"].memberID,
		Type:           rewards.Bonus,
		Amount:         10,
		Reason:         "Helped a sibling with homework",
		ActorMemberID:  s.sam().memberID,
		IdempotencyKey: "seed:bonus:ava",
	})
	return err
}

// bonusSpec is one parent-posted bonus task. A zero-length showClock means the task is open
// from the start of its day, and an empty endClock means the end of the day.
type bonusSpec struct {
	title       string
	description string
	points      int
	claimMode   string
	showDay     int
	showClock   string
	endDay      int
	endClock    string
	claims      []bonusClaimSpec
}

// bonusClaimSpec is one child's claim, made on the task's show day at clock. Its timestamps
// follow the status, the same way the bonus package writes them.
type bonusClaimSpec struct {
	login  string
	status string
	clock  string
	note   string
}

// bonusSpecs mirror the mock's bonus page. Points are the mock's values and are not added to
// the ledger, so the seeded balances stay as they are: only approved claims post awards.
var bonusSpecs = []bonusSpec{
	{
		title: "Wash the car", description: "Hand wash and vacuum inside", points: 20, claimMode: "first_come",
		showDay: 0, showClock: "16:00", endDay: 0, endClock: "20:00",
		claims: []bonusClaimSpec{{login: "ava", status: "claimed", clock: "16:05"}},
	},
	{
		title: "Fold the laundry", description: "Fold and sort the clean basket", points: 10, claimMode: "everyone_once",
		showDay: 0, endDay: 0,
		claims: []bonusClaimSpec{{login: "noah", status: "submitted", clock: "16:20"}},
	},
	{
		title: "Water the garden", description: "Front and back beds", points: 8, claimMode: "first_come",
		showDay: 0, showClock: "16:00", endDay: 0,
	},
	{
		title: "Clean the garage", points: 25, claimMode: "first_come",
		showDay: 1, showClock: "09:00", endDay: 1, endClock: "17:00",
	},
	{
		title: "Wash the windows", points: 12, claimMode: "everyone_once",
		showDay: 2, showClock: "00:00", endDay: 2,
	},
	{
		title: "Rake the leaves", points: 15, claimMode: "everyone_once",
		showDay: -3, showClock: "09:00", endDay: -3,
		claims: []bonusClaimSpec{{login: "noah", status: "rejected", clock: "17:00", note: "Leaves still on the path"}},
	},
	{
		title: "Sweep the porch", points: 5, claimMode: "first_come",
		showDay: -1, showClock: "08:00", endDay: -1, endClock: "18:00",
		claims: []bonusClaimSpec{{login: "mia", status: "released", clock: "10:00"}},
	},
}

func (s *seeder) addBonusTasks() error {
	for _, spec := range bonusSpecs {
		if err := s.addBonusTask(spec); err != nil {
			return fmt.Errorf("%s: %w", spec.title, err)
		}
	}
	return nil
}

// addBonusTask writes a task offered to every child, then its claims. Approval is required,
// as on the mock.
func (s *seeder) addBonusTask(spec bonusSpec) error {
	var showFrom any
	if spec.showClock != "" {
		from, err := s.at(spec.showDay, spec.showClock)
		if err != nil {
			return err
		}
		showFrom = stamp(from)
	}
	endsAt, err := s.at(spec.endDay, spec.endClock)
	if err != nil {
		return err
	}

	taskID := ids.New()
	if _, err := s.tx.ExecContext(s.ctx,
		`INSERT INTO bonus_tasks (id, family_id, template_id, title, description, points, claim_mode,
			approval_required, show_from, ends_at, created_by)
		 VALUES (?, ?, NULL, ?, ?, ?, ?, 1, ?, ?, ?)`,
		taskID, s.familyID, spec.title, nilIfEmpty(spec.description), spec.points, spec.claimMode,
		showFrom, stamp(endsAt), s.sam().userID,
	); err != nil {
		return err
	}
	for _, login := range []string{"ava", "noah", "mia"} {
		if _, err := s.tx.ExecContext(s.ctx,
			`INSERT INTO bonus_task_members (id, family_id, bonus_task_id, family_member_id)
			 VALUES (?, ?, ?, ?)`,
			ids.New(), s.familyID, taskID, s.members[login].memberID,
		); err != nil {
			return err
		}
	}
	for _, c := range spec.claims {
		if err := s.addBonusClaim(taskID, spec, c); err != nil {
			return fmt.Errorf("%s claim: %w", c.login, err)
		}
	}
	return nil
}

// addBonusClaim writes one claim with the timestamps its status needs. It posts no award, so the
// seeded balances do not change.
func (s *seeder) addBonusClaim(taskID string, spec bonusSpec, c bonusClaimSpec) error {
	at, err := s.at(spec.showDay, c.clock)
	if err != nil {
		return err
	}
	claimed := s.clamp(at)

	var released, completed, decided, decidedBy, note any
	switch c.status {
	case "claimed":
	case "released":
		released = stamp(s.clamp(at.Add(30 * time.Minute)))
	case "submitted":
		completed = stamp(s.clamp(at.Add(30 * time.Minute)))
	case "rejected":
		completed = stamp(s.clamp(at.Add(30 * time.Minute)))
		decided = stamp(s.clamp(at.Add(time.Hour)))
		decidedBy = s.sam().memberID
		note = c.note
	default:
		return fmt.Errorf("unknown bonus claim status %q", c.status)
	}

	_, err = s.tx.ExecContext(s.ctx,
		`INSERT INTO bonus_claims (id, family_id, bonus_task_id, family_member_id, claim_mode, status,
			claimed_at, released_at, completed_at, decided_at, decided_by_member_id, decision_note, version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		ids.New(), s.familyID, taskID, s.members[c.login].memberID, spec.claimMode, c.status,
		stamp(claimed), released, completed, decided, decidedBy, note,
	)
	return err
}

func (s *seeder) addRedemptions() error {
	for _, r := range redemptionSpecs {
		if err := s.addRedemption(r); err != nil {
			return fmt.Errorf("%s %s: %w", r.login, r.reward, err)
		}
	}
	return nil
}

// addRedemption writes a request the way the rewards package does. A pending or approved
// request holds its points. A rejected request also gets a reversal, which gives them back.
func (s *seeder) addRedemption(r redemptionSpec) error {
	spec, err := findReward(r.reward)
	if err != nil {
		return err
	}
	member := s.members[r.login].memberID
	requestedAt, err := s.at(-r.daysAgo, "17:00")
	if err != nil {
		return err
	}
	requested := s.clamp(requestedAt)

	var decidedAt, decidedBy, reason any
	var decided time.Time
	if r.status != "pending" {
		decided = s.clamp(requested.Add(time.Hour))
		decidedAt = stamp(decided)
		decidedBy = s.sam().memberID
	}
	if r.status == "rejected" {
		reason = r.reason
	}

	id := ids.New()
	if _, err := s.tx.ExecContext(s.ctx,
		`INSERT INTO redemptions (id, family_id, reward_id, family_member_id, status, cost,
			idempotency_key, requested_at, decided_at, decided_by_member_id, decision_reason, version, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		id, s.familyID, s.rewardIDs[spec.key], member, r.status, spec.cost,
		"seed-"+id, stamp(requested), decidedAt, decidedBy, reason, stamp(requested),
	); err != nil {
		return err
	}
	if _, err := rewards.Post(s.ctx, s.tx, requested, rewards.Entry{
		FamilyID:       s.familyID,
		MemberID:       member,
		Type:           rewards.Redemption,
		Amount:         -spec.cost,
		ReferenceType:  "redemption",
		ReferenceID:    id,
		ActorMemberID:  member,
		IdempotencyKey: "redemption:" + id,
	}); err != nil {
		return err
	}
	if r.status == "rejected" {
		if _, err := rewards.Post(s.ctx, s.tx, decided, rewards.Entry{
			FamilyID:       s.familyID,
			MemberID:       member,
			Type:           rewards.RedemptionReversal,
			Amount:         spec.cost,
			ReferenceType:  "redemption",
			ReferenceID:    id,
			ActorMemberID:  s.sam().memberID,
			IdempotencyKey: "redemption-reversal:" + id,
		}); err != nil {
			return err
		}
	}
	return nil
}

func findReward(key string) (rewardSpec, error) {
	for _, r := range rewardSpecs {
		if r.key == key {
			return r, nil
		}
	}
	return rewardSpec{}, fmt.Errorf("unknown reward %q", key)
}

func (s *seeder) insertDefinition(d definition, createdBy string) (string, error) {
	id := ids.New()
	var penaltyValue, penaltyUnit any
	if d.penalty != nil {
		penaltyValue = d.penalty.value
		penaltyUnit = d.penalty.unit
	}
	_, err := s.tx.ExecContext(s.ctx,
		`INSERT INTO task_definitions (id, family_id, template_id, title, category_id, part_of_day, time_zone, starts_on, due_time, recurrence_rule, points, priority, approval_required, expires_after_minutes, penalty_value, penalty_unit, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'normal', ?, ?, ?, ?, ?)`,
		id, s.familyID, d.templateID, d.title, d.categoryID, d.partOfDay, TimeZone, d.startsOn,
		nilIfEmpty(d.dueTime), nilIfEmpty(d.rule), d.points, boolInt(d.approval),
		d.expiresAfter, penaltyValue, penaltyUnit, createdBy)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *seeder) addDefinitionAssignee(defID, login string) error {
	_, err := s.tx.ExecContext(s.ctx,
		`INSERT INTO task_definition_assignees (id, family_id, task_definition_id, family_member_id) VALUES (?, ?, ?, ?)`,
		ids.New(), s.familyID, defID, s.members[login].memberID)
	return err
}

// insertOccurrence inserts an occurrence as scheduled. MarkPastDue and MarkMissed
// set its final status later.
func (s *seeder) insertOccurrence(defID string, offset int, dueClock string, expires *time.Time) (string, error) {
	due, err := s.at(offset, dueClock)
	if err != nil {
		return "", err
	}
	var expiresAt *string
	if expires != nil {
		t := stamp(*expires)
		expiresAt = &t
	}
	id := ids.New()
	if _, err := s.tx.ExecContext(s.ctx,
		`INSERT INTO task_occurrences (id, family_id, task_definition_id, due_date, due_at, expires_at, status)
		 VALUES (?, ?, ?, ?, ?, ?, 'scheduled')`,
		id, s.familyID, defID, s.day(offset), stamp(due), expiresAt); err != nil {
		return "", err
	}
	return id, nil
}

func (s *seeder) occurrenceOn(defID string, offset int) (string, error) {
	var id string
	err := s.tx.QueryRowContext(s.ctx,
		`SELECT id FROM task_occurrences WHERE family_id = ? AND task_definition_id = ? AND due_date = ?`,
		s.familyID, defID, s.day(offset)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("occurrence for day %d: %w", offset, err)
	}
	return id, nil
}

func (s *seeder) assign(occ, login string) error {
	_, err := s.tx.ExecContext(s.ctx,
		`INSERT INTO task_occurrence_assignees (id, family_id, task_occurrence_id, family_member_id, status, version)
		 VALUES (?, ?, ?, ?, 'to_do', 1)`,
		ids.New(), s.familyID, occ, s.members[login].memberID)
	return err
}

// applyState sets an assignee's state. The clock is used only for submitted and
// approved states. Stamps are clamped to be at least two minutes before now.
func (s *seeder) applyState(occ, login, state string, offset int, clock string) error {
	member := s.members[login].memberID
	switch state {
	case stateToDo:
		return nil
	case stateSubmitted:
		when, err := s.at(offset, clock)
		if err != nil {
			return err
		}
		_, err = s.tx.ExecContext(s.ctx,
			`UPDATE task_occurrence_assignees SET status = 'submitted', completed_at = ?
			 WHERE family_id = ? AND task_occurrence_id = ? AND family_member_id = ?`,
			stamp(s.clamp(when)), s.familyID, occ, member)
		return err
	case stateApproved:
		when, err := s.at(offset, clock)
		if err != nil {
			return err
		}
		done := s.clamp(when)
		decided := s.clamp(done.Add(10 * time.Minute))
		_, err = s.tx.ExecContext(s.ctx,
			`UPDATE task_occurrence_assignees
			 SET status = 'approved', completed_at = ?, decided_at = ?, decided_by_member_id = ?
			 WHERE family_id = ? AND task_occurrence_id = ? AND family_member_id = ?`,
			stamp(done), stamp(decided), s.sam().memberID, s.familyID, occ, member)
		return err
	default:
		return fmt.Errorf("unknown state %q", state)
	}
}

// clamp keeps a stamp at least two minutes before now, so no state looks like it
// happened in the future.
func (s *seeder) clamp(t time.Time) time.Time {
	latest := s.now.Add(-2 * time.Minute)
	if t.After(latest) {
		return latest
	}
	return t
}

// at returns the UTC instant of a local clock time on a day offset from today. An
// empty clock means the end of the day (23:59:59), the same as a task with no due time.
func (s *seeder) at(offset int, clock string) (time.Time, error) {
	day := s.today.AddDate(0, 0, offset)
	hour, minute, second := 23, 59, 59
	if clock != "" {
		t, err := time.Parse(clockLayout, clock)
		if err != nil {
			return time.Time{}, fmt.Errorf("bad clock %q: %w", clock, err)
		}
		hour, minute, second = t.Hour(), t.Minute(), 0
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, second, 0, s.zone).UTC(), nil
}

func (s *seeder) day(offset int) string {
	return s.today.AddDate(0, 0, offset).Format(dateLayout)
}

func familySettings(ctx context.Context, tx *sql.Tx) (string, bool, error) {
	var settings string
	err := tx.QueryRowContext(ctx, `SELECT settings FROM families LIMIT 1`).Scan(&settings)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return settings, true, nil
}

func isDemo(settings string) bool {
	var m map[string]string
	if err := json.Unmarshal([]byte(settings), &m); err != nil {
		return false
	}
	return m["demo_seed"] == marker
}

func newPassword() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("demo: generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func findTemplate(name string) (templateSpec, error) {
	for _, t := range templateSpecs {
		if t.name == name {
			return t, nil
		}
	}
	return templateSpec{}, fmt.Errorf("unknown template %q", name)
}

// stamp formats an instant the way the rest of the database stores timestamps.
func stamp(t time.Time) string {
	return t.UTC().Format(dbTimeLayout) + "+00:00"
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func intPtr(v int) *int {
	return &v
}
