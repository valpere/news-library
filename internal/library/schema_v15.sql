CREATE TABLE trash_entries (
 item_id INTEGER PRIMARY KEY REFERENCES items(id),
 reason TEXT NOT NULL, moved_at TEXT NOT NULL
);
CREATE VIEW active_items AS SELECT * FROM items
 WHERE NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=items.id);
INSERT INTO trash_entries(item_id,reason,moved_at)
 SELECT i.id,i.error,strftime('%Y-%m-%dT%H:%M:%fZ','now') FROM items i
 WHERE i.state='needs_input' AND i.error IN ('джерело повернуло HTTP 404','джерело повернуло HTTP 410')
 AND i.canonical_url NOT LIKE '%youtube.com/%' AND i.canonical_url NOT LIKE '%youtu.be/%'
 AND NOT EXISTS(SELECT 1 FROM source_versions WHERE item_id=i.id)
 AND NOT EXISTS(SELECT 1 FROM rating_events WHERE item_id=i.id)
 AND NOT EXISTS(SELECT 1 FROM jobs WHERE item_id=i.id AND state IN ('queued','running','retry_wait'));
DELETE FROM item_fts WHERE rowid IN (SELECT item_id FROM trash_entries);
PRAGMA user_version=15;
