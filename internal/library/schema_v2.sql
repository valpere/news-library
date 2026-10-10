ALTER TABLE jobs ADD COLUMN stage TEXT NOT NULL DEFAULT 'fetch' CHECK(stage IN ('fetch','digest'));
ALTER TABLE jobs ADD COLUMN snapshot_id INTEGER REFERENCES source_versions(id);
ALTER TABLE jobs ADD COLUMN config_version TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN prompt_version TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN input_limit INTEGER NOT NULL DEFAULT 0;
DROP INDEX one_active_fetch;
CREATE UNIQUE INDEX one_active_fetch ON jobs(item_id) WHERE stage='fetch' AND state IN ('queued','running','retry_wait');
CREATE UNIQUE INDEX one_digest_job ON jobs(snapshot_id,config_version) WHERE stage='digest';
CREATE TABLE generated_artifacts (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id),
 snapshot_id INTEGER NOT NULL REFERENCES source_versions(id), job_id INTEGER NOT NULL UNIQUE REFERENCES jobs(id),
 markdown TEXT NOT NULL, model TEXT NOT NULL, actual_model TEXT NOT NULL, provider TEXT NOT NULL,
 prompt_version TEXT NOT NULL, created_at TEXT NOT NULL, input_tokens INTEGER NOT NULL DEFAULT 0,
 output_tokens INTEGER NOT NULL DEFAULT 0
);
CREATE TRIGGER artifact_source_owner BEFORE INSERT ON generated_artifacts
WHEN NOT EXISTS(SELECT 1 FROM source_versions WHERE id=NEW.snapshot_id AND item_id=NEW.item_id)
BEGIN SELECT RAISE(ABORT,'artifact snapshot belongs to another item'); END;
CREATE TRIGGER immutable_artifact_update BEFORE UPDATE ON generated_artifacts BEGIN SELECT RAISE(ABORT,'immutable artifact'); END;
CREATE TRIGGER immutable_artifact_delete BEFORE DELETE ON generated_artifacts BEGIN SELECT RAISE(ABORT,'immutable artifact'); END;
DROP TRIGGER immutable_rating_update;
DROP TRIGGER immutable_rating_delete;
DROP TRIGGER rating_source_owner;
ALTER TABLE rating_events RENAME TO rating_events_v1;
CREATE TABLE rating_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, item_id INTEGER NOT NULL REFERENCES items(id),
 score INTEGER NOT NULL CHECK(typeof(score)='integer' AND score>=0), recorded_at TEXT NOT NULL,
 assessed_at TEXT, source_version_id INTEGER REFERENCES source_versions(id),
 digest_id INTEGER REFERENCES generated_artifacts(id), reason TEXT NOT NULL
);
INSERT INTO rating_events SELECT * FROM rating_events_v1;
DROP TABLE rating_events_v1;
CREATE TRIGGER immutable_rating_update BEFORE UPDATE ON rating_events BEGIN SELECT RAISE(ABORT,'immutable rating'); END;
CREATE TRIGGER immutable_rating_delete BEFORE DELETE ON rating_events BEGIN SELECT RAISE(ABORT,'immutable rating'); END;
CREATE TRIGGER rating_source_owner BEFORE INSERT ON rating_events
WHEN NEW.source_version_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM source_versions WHERE id=NEW.source_version_id AND item_id=NEW.item_id)
BEGIN SELECT RAISE(ABORT,'snapshot belongs to another item'); END;
CREATE TRIGGER rating_digest_owner BEFORE INSERT ON rating_events
WHEN NEW.digest_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM generated_artifacts WHERE id=NEW.digest_id AND item_id=NEW.item_id AND (NEW.source_version_id IS NULL OR snapshot_id=NEW.source_version_id))
BEGIN SELECT RAISE(ABORT,'digest context mismatch'); END;
PRAGMA user_version = 2;
