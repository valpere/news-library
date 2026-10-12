CREATE TABLE knowledge_runs (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id),
 snapshot_id INTEGER NOT NULL REFERENCES source_versions(id), digest_id INTEGER NOT NULL REFERENCES generated_artifacts(id),
 model TEXT NOT NULL, prompt_version TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','retry_wait','done','failed','cancelled')),
 attempts INTEGER NOT NULL DEFAULT 0, token TEXT NOT NULL DEFAULT '', lease_until INTEGER NOT NULL DEFAULT 0,
 next_attempt INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '',
 linked_count INTEGER NOT NULL DEFAULT 0, UNIQUE(digest_id,prompt_version,model)
);
CREATE INDEX knowledge_current ON knowledge_runs(item_id,snapshot_id,updated_at DESC,id DESC);
CREATE TRIGGER knowledge_run_owner BEFORE INSERT ON knowledge_runs
WHEN NOT EXISTS(SELECT 1 FROM generated_artifacts d WHERE d.id=NEW.digest_id AND d.item_id=NEW.item_id AND d.snapshot_id=NEW.snapshot_id)
BEGIN SELECT RAISE(ABORT,'knowledge source ownership mismatch'); END;
CREATE TABLE article_knowledge (
 id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL UNIQUE REFERENCES knowledge_runs(id),
 item_id INTEGER NOT NULL REFERENCES items(id), snapshot_id INTEGER NOT NULL REFERENCES source_versions(id),
 digest_id INTEGER NOT NULL REFERENCES generated_artifacts(id), category TEXT NOT NULL,
 tags_json TEXT NOT NULL, questions_json TEXT NOT NULL, search_text TEXT NOT NULL, markdown_path TEXT NOT NULL, hash TEXT NOT NULL,
 model TEXT NOT NULL, prompt_version TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE INDEX article_knowledge_source ON article_knowledge(item_id,snapshot_id);
CREATE TRIGGER knowledge_metadata_owner BEFORE INSERT ON article_knowledge
WHEN NOT EXISTS(SELECT 1 FROM knowledge_runs r WHERE r.id=NEW.run_id AND r.item_id=NEW.item_id AND r.snapshot_id=NEW.snapshot_id AND r.digest_id=NEW.digest_id)
BEGIN SELECT RAISE(ABORT,'knowledge metadata ownership mismatch'); END;
CREATE TRIGGER immutable_article_knowledge_update BEFORE UPDATE ON article_knowledge BEGIN SELECT RAISE(ABORT,'immutable knowledge'); END;
CREATE TRIGGER immutable_article_knowledge_delete BEFORE DELETE ON article_knowledge BEGIN SELECT RAISE(ABORT,'immutable knowledge'); END;
CREATE TABLE knowledge_failures (
 id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL REFERENCES knowledge_runs(id), kind TEXT NOT NULL,
 message TEXT NOT NULL, recorded_at TEXT NOT NULL, response_path TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL DEFAULT ''
);
CREATE TRIGGER immutable_knowledge_failure_update BEFORE UPDATE ON knowledge_failures BEGIN SELECT RAISE(ABORT,'immutable failure'); END;
CREATE TRIGGER immutable_knowledge_failure_delete BEFORE DELETE ON knowledge_failures BEGIN SELECT RAISE(ABORT,'immutable failure'); END;
ALTER TABLE connection_versions ADD COLUMN author TEXT NOT NULL DEFAULT 'user';
ALTER TABLE connection_versions ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE connection_versions ADD COLUMN prompt_version TEXT NOT NULL DEFAULT '';
PRAGMA user_version=11;
