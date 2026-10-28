-- Family-wide clock format: '12h' (AM/PM) or '24h'. Every member and the wall display use it.
ALTER TABLE families ADD COLUMN time_format TEXT NOT NULL DEFAULT '12h'
    CHECK (time_format IN ('12h', '24h'));
