CREATE TABLE IF NOT EXISTS items (
 id INTEGER PRIMARY KEY, original_url TEXT NOT NULL, canonical_url TEXT NOT NULL UNIQUE,
 title TEXT NOT NULL DEFAULT '', added_at TEXT NOT NULL,
 placement TEXT NOT NULL DEFAULT 'inbox' CHECK(placement IN ('inbox','library','deferred')),
 is_read INTEGER NOT NULL DEFAULT 0 CHECK(is_read IN (0,1)),
 state TEXT NOT NULL DEFAULT 'queued', error TEXT NOT NULL DEFAULT '', selected_snapshot INTEGER
);
CREATE TABLE IF NOT EXISTS source_aliases (
 url TEXT PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id)
);
CREATE TABLE IF NOT EXISTS jobs (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id),
 state TEXT NOT NULL DEFAULT 'queued', attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt INTEGER NOT NULL DEFAULT 0, lease_until INTEGER NOT NULL DEFAULT 0,
 token TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_fetch ON jobs(item_id) WHERE state IN ('queued','running','retry_wait');
CREATE TABLE IF NOT EXISTS source_versions (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id),
 job_id INTEGER UNIQUE REFERENCES jobs(id), text TEXT NOT NULL, title TEXT NOT NULL,
 hash TEXT NOT NULL, retrieved_at TEXT NOT NULL, origin TEXT NOT NULL,
 extractor TEXT NOT NULL, completeness TEXT NOT NULL, warning TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS rating_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, item_id INTEGER NOT NULL REFERENCES items(id),
 score INTEGER NOT NULL CHECK(typeof(score)='integer' AND score>=0), recorded_at TEXT NOT NULL,
 assessed_at TEXT, source_version_id INTEGER REFERENCES source_versions(id),
 digest_id INTEGER CHECK(digest_id IS NULL), reason TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS immutable_rating_update BEFORE UPDATE ON rating_events BEGIN SELECT RAISE(ABORT,'immutable rating'); END;
CREATE TRIGGER IF NOT EXISTS immutable_rating_delete BEFORE DELETE ON rating_events BEGIN SELECT RAISE(ABORT,'immutable rating'); END;
CREATE TRIGGER IF NOT EXISTS rating_source_owner BEFORE INSERT ON rating_events
WHEN NEW.source_version_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM source_versions WHERE id=NEW.source_version_id AND item_id=NEW.item_id)
BEGIN SELECT RAISE(ABORT,'snapshot belongs to another item'); END;
CREATE TRIGGER IF NOT EXISTS immutable_snapshot_update BEFORE UPDATE ON source_versions BEGIN SELECT RAISE(ABORT,'immutable snapshot'); END;
CREATE TRIGGER IF NOT EXISTS immutable_snapshot_delete BEFORE DELETE ON source_versions BEGIN SELECT RAISE(ABORT,'immutable snapshot'); END;
CREATE TABLE IF NOT EXISTS item_tags (
 item_id INTEGER NOT NULL REFERENCES items(id), tag TEXT NOT NULL, origin TEXT NOT NULL DEFAULT 'human',
 PRIMARY KEY(item_id,tag,origin)
);
CREATE VIRTUAL TABLE IF NOT EXISTS item_fts USING fts5(title, text, url, tokenize='unicode61');
PRAGMA user_version = 1;
