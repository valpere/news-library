CREATE TABLE search_events (
 id INTEGER PRIMARY KEY,
 query TEXT NOT NULL, mode TEXT NOT NULL, actual_mode TEXT NOT NULL,
 filters TEXT NOT NULL, result_ids TEXT NOT NULL,
 created_at TEXT NOT NULL, not_found_at TEXT
);
CREATE TABLE search_judgments (
 id INTEGER PRIMARY KEY,
 event_id INTEGER NOT NULL REFERENCES search_events(id),
 item_id INTEGER NOT NULL REFERENCES items(id),
 score INTEGER NOT NULL CHECK(score BETWEEN 0 AND 2),
 recorded_at TEXT NOT NULL
);
CREATE INDEX search_judgments_event ON search_judgments(event_id,item_id,id);
PRAGMA user_version=16;
