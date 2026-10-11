ALTER TABLE cache_entries ADD COLUMN response_headers TEXT NOT NULL DEFAULT '{}';
ALTER TABLE cache_entries ADD COLUMN checked_at TEXT NOT NULL DEFAULT '';
PRAGMA user_version=6;
