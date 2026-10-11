CREATE TABLE connection_notes (
 id INTEGER PRIMARY KEY, stable_id TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL
);
CREATE TABLE connection_versions (
 id INTEGER PRIMARY KEY, note_id INTEGER NOT NULL REFERENCES connection_notes(id),
 revision INTEGER NOT NULL, title TEXT NOT NULL, category TEXT NOT NULL,
 tags_json TEXT NOT NULL, questions_json TEXT NOT NULL, body TEXT NOT NULL, search_text TEXT NOT NULL,
 markdown_path TEXT NOT NULL, hash TEXT NOT NULL, created_at TEXT NOT NULL,
 UNIQUE(note_id,revision)
);
CREATE TABLE connection_members (
 version_id INTEGER NOT NULL REFERENCES connection_versions(id),
 item_id INTEGER NOT NULL REFERENCES items(id), snapshot_id INTEGER NOT NULL REFERENCES source_versions(id),
 digest_id INTEGER REFERENCES generated_artifacts(id), role TEXT NOT NULL,
 PRIMARY KEY(version_id,item_id)
);
CREATE INDEX connection_backlinks ON connection_members(item_id,version_id);
CREATE TRIGGER connection_member_owner BEFORE INSERT ON connection_members
WHEN NOT EXISTS(SELECT 1 FROM source_versions WHERE id=NEW.snapshot_id AND item_id=NEW.item_id)
 OR (NEW.digest_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM generated_artifacts WHERE id=NEW.digest_id AND item_id=NEW.item_id AND snapshot_id=NEW.snapshot_id))
BEGIN SELECT RAISE(ABORT,'connection source ownership mismatch'); END;
CREATE TRIGGER immutable_connection_version_update BEFORE UPDATE ON connection_versions BEGIN SELECT RAISE(ABORT,'immutable connection version'); END;
CREATE TRIGGER immutable_connection_version_delete BEFORE DELETE ON connection_versions BEGIN SELECT RAISE(ABORT,'immutable connection version'); END;
CREATE TRIGGER immutable_connection_member_update BEFORE UPDATE ON connection_members BEGIN SELECT RAISE(ABORT,'immutable connection member'); END;
CREATE TRIGGER immutable_connection_member_delete BEFORE DELETE ON connection_members BEGIN SELECT RAISE(ABORT,'immutable connection member'); END;
PRAGMA user_version=9;
