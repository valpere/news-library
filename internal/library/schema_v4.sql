ALTER TABLE items ADD COLUMN filter_state TEXT NOT NULL DEFAULT 'not_checked' CHECK(filter_state IN ('not_checked','passed','rejected','overridden'));
ALTER TABLE jobs ADD COLUMN filter_override INTEGER NOT NULL DEFAULT 0 CHECK(filter_override IN (0,1));
ALTER TABLE jobs ADD COLUMN interests TEXT NOT NULL DEFAULT '';
CREATE TABLE filter_rule_versions (version TEXT PRIMARY KEY,config TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TRIGGER immutable_rules_update BEFORE UPDATE ON filter_rule_versions BEGIN SELECT RAISE(ABORT,'immutable rules'); END;
CREATE TRIGGER immutable_rules_delete BEFORE DELETE ON filter_rule_versions BEGIN SELECT RAISE(ABORT,'immutable rules'); END;
CREATE TABLE filter_decisions (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id), snapshot_id INTEGER REFERENCES source_versions(id),
 rules_version TEXT NOT NULL REFERENCES filter_rule_versions(version), stage TEXT NOT NULL CHECK(stage IN ('url','source')),
 result TEXT NOT NULL CHECK(result IN ('passed','rejected','overridden')), probability REAL NOT NULL CHECK(probability BETWEEN 0 AND 1),
 threshold REAL NOT NULL CHECK(threshold BETWEEN 0 AND 1), contributions TEXT NOT NULL, created_at TEXT NOT NULL,
 override_of INTEGER REFERENCES filter_decisions(id)
);
CREATE TRIGGER filter_snapshot_owner BEFORE INSERT ON filter_decisions
WHEN NEW.snapshot_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM source_versions WHERE id=NEW.snapshot_id AND item_id=NEW.item_id)
BEGIN SELECT RAISE(ABORT,'filter snapshot belongs to another item'); END;
CREATE TRIGGER immutable_filter_update BEFORE UPDATE ON filter_decisions BEGIN SELECT RAISE(ABORT,'immutable decision'); END;
CREATE TRIGGER immutable_filter_delete BEFORE DELETE ON filter_decisions BEGIN SELECT RAISE(ABORT,'immutable decision'); END;
CREATE INDEX filter_history ON filter_decisions(item_id,id DESC);
CREATE TABLE digest_feedback (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id), digest_id INTEGER NOT NULL REFERENCES generated_artifacts(id),
 quality TEXT NOT NULL CHECK(quality IN ('good','inaccurate','incomplete','unclear')), note TEXT NOT NULL, recorded_at TEXT NOT NULL
);
CREATE TRIGGER feedback_digest_owner BEFORE INSERT ON digest_feedback
WHEN NOT EXISTS(SELECT 1 FROM generated_artifacts WHERE id=NEW.digest_id AND item_id=NEW.item_id)
BEGIN SELECT RAISE(ABORT,'feedback digest belongs to another item'); END;
CREATE TRIGGER immutable_feedback_update BEFORE UPDATE ON digest_feedback BEGIN SELECT RAISE(ABORT,'immutable feedback'); END;
CREATE TRIGGER immutable_feedback_delete BEFORE DELETE ON digest_feedback BEGIN SELECT RAISE(ABORT,'immutable feedback'); END;
CREATE TABLE interest_predictions (
 digest_id INTEGER PRIMARY KEY REFERENCES generated_artifacts(id), probability REAL NOT NULL CHECK(probability BETWEEN 0 AND 1),
 reason TEXT NOT NULL, profile TEXT NOT NULL, profile_hash TEXT NOT NULL
);
CREATE TRIGGER immutable_prediction_update BEFORE UPDATE ON interest_predictions BEGIN SELECT RAISE(ABORT,'immutable prediction'); END;
CREATE TRIGGER immutable_prediction_delete BEFORE DELETE ON interest_predictions BEGIN SELECT RAISE(ABORT,'immutable prediction'); END;
PRAGMA user_version=4;
