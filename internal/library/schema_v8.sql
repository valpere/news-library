CREATE TABLE deliveries (
 id INTEGER PRIMARY KEY, chat_id TEXT NOT NULL, bot_id TEXT NOT NULL,
 trigger TEXT NOT NULL CHECK(trigger IN ('manual','schedule')), slot TEXT UNIQUE,
 state TEXT NOT NULL CHECK(state IN ('preview','queued','sending','sent','failed','unknown','missed')),
 created_at TEXT NOT NULL, error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE delivery_parts (
 delivery_id INTEGER NOT NULL REFERENCES deliveries(id), ordinal INTEGER NOT NULL,
 item_id INTEGER NOT NULL REFERENCES items(id), snapshot_id INTEGER NOT NULL REFERENCES source_versions(id),
 digest_id INTEGER NOT NULL REFERENCES generated_artifacts(id), text_path TEXT NOT NULL, hash TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','sending','sent','failed','unknown')),
 message_id INTEGER, token TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL,
 PRIMARY KEY(delivery_id,ordinal)
);
CREATE TRIGGER delivery_source_owner BEFORE INSERT ON delivery_parts
WHEN NOT EXISTS(SELECT 1 FROM generated_artifacts WHERE id=NEW.digest_id AND item_id=NEW.item_id AND snapshot_id=NEW.snapshot_id)
BEGIN SELECT RAISE(ABORT,'delivery source ownership mismatch'); END;
PRAGMA user_version=8;
