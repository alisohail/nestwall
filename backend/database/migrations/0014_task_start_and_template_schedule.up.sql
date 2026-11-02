-- Optional start time on a task, and the schedule a template remembers: its repeat settings and
-- its checklist. repeat_state is the repeat builder state as JSON. checklist is a JSON array of
-- step texts. A template applies today's date, so no date is stored.
ALTER TABLE task_definitions ADD COLUMN start_time TEXT CHECK (start_time IS NULL OR start_time GLOB '[0-2][0-9]:[0-5][0-9]');
ALTER TABLE task_templates ADD COLUMN start_time TEXT CHECK (start_time IS NULL OR start_time GLOB '[0-2][0-9]:[0-5][0-9]');
ALTER TABLE task_templates ADD COLUMN repeat_state TEXT CHECK (repeat_state IS NULL OR length(repeat_state) <= 2000);
ALTER TABLE task_templates ADD COLUMN checklist TEXT CHECK (checklist IS NULL OR length(checklist) <= 20000);
