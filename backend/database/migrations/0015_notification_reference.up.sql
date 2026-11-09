-- Where a notification came from, so the bell can open its source. reference_type names the kind of
-- record (for example task_occurrence, bonus_claim or redemption) and reference_id is its ID. Older
-- notifications have neither, and the bell leaves them without a link.
ALTER TABLE notifications ADD COLUMN reference_type TEXT;
ALTER TABLE notifications ADD COLUMN reference_id TEXT;
