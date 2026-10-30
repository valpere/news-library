-- Baseline schema of news-library 0.1.0 (no migrations, no earlier versions).
-- Changes to the model edit this file; databases of other builds are refused by Open.
CREATE TABLE items (
 id INTEGER PRIMARY KEY, original_url TEXT NOT NULL, canonical_url TEXT NOT NULL UNIQUE,
 title TEXT NOT NULL DEFAULT '', added_at TEXT NOT NULL,
 is_read INTEGER NOT NULL DEFAULT 0 CHECK(is_read IN (0,1)),
 state TEXT NOT NULL DEFAULT 'queued', error TEXT NOT NULL DEFAULT '', selected_snapshot INTEGER,
 rating INTEGER CHECK(rating BETWEEN 0 AND 6), rated_at TEXT CHECK(rated_at IS NULL OR rating IS NOT NULL),
 filter_state TEXT NOT NULL DEFAULT 'not_checked' CHECK(filter_state IN ('not_checked','passed','rejected','overridden')));
CREATE TABLE source_aliases (
 url TEXT PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id)
);
CREATE TABLE jobs (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id),
 state TEXT NOT NULL DEFAULT 'queued', attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt INTEGER NOT NULL DEFAULT 0, lease_until INTEGER NOT NULL DEFAULT 0,
 token TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT ''
, stage TEXT NOT NULL DEFAULT 'fetch' CHECK(stage IN ('fetch','digest')), snapshot_id INTEGER REFERENCES source_versions(id), config_version TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '', prompt_version TEXT NOT NULL DEFAULT '', input_limit INTEGER NOT NULL DEFAULT 0, refresh INTEGER NOT NULL DEFAULT 0, filter_override INTEGER NOT NULL DEFAULT 0 CHECK(filter_override IN (0,1)), interests TEXT NOT NULL DEFAULT '', chunk_limit INTEGER NOT NULL DEFAULT 0, chunk_max_parts INTEGER NOT NULL DEFAULT 0);
CREATE TABLE source_versions (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id),
 job_id INTEGER UNIQUE REFERENCES jobs(id), title TEXT NOT NULL,
 hash TEXT NOT NULL, retrieved_at TEXT NOT NULL, origin TEXT NOT NULL,
 extractor TEXT NOT NULL, completeness TEXT NOT NULL, warning TEXT NOT NULL
, text_path TEXT NOT NULL DEFAULT '');
CREATE TABLE item_tags (
 item_id INTEGER NOT NULL REFERENCES items(id), tag TEXT NOT NULL, origin TEXT NOT NULL DEFAULT 'human',
 PRIMARY KEY(item_id,tag,origin)
);
CREATE TABLE generated_artifacts (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id),
 snapshot_id INTEGER NOT NULL REFERENCES source_versions(id), job_id INTEGER NOT NULL UNIQUE REFERENCES jobs(id),
 model TEXT NOT NULL, actual_model TEXT NOT NULL, provider TEXT NOT NULL,
 prompt_version TEXT NOT NULL, created_at TEXT NOT NULL, input_tokens INTEGER NOT NULL DEFAULT 0,
 output_tokens INTEGER NOT NULL DEFAULT 0
, markdown_path TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL DEFAULT '', chunk_count INTEGER NOT NULL DEFAULT 1);
CREATE VIRTUAL TABLE item_fts USING fts5(title,text,url,content='',contentless_delete=1,tokenize='unicode61');
CREATE TABLE cache_entries (url TEXT PRIMARY KEY,final_url TEXT NOT NULL,content_type TEXT NOT NULL,body_path TEXT NOT NULL,hash TEXT NOT NULL,fetched_at TEXT NOT NULL,expires_at INTEGER NOT NULL,partial INTEGER NOT NULL, last_accessed INTEGER NOT NULL DEFAULT 0, response_headers TEXT NOT NULL DEFAULT '{}', checked_at TEXT NOT NULL DEFAULT '');
CREATE TABLE filter_rule_versions (version TEXT PRIMARY KEY,config TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE filter_decisions (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id), snapshot_id INTEGER REFERENCES source_versions(id),
 rules_version TEXT NOT NULL REFERENCES filter_rule_versions(version), stage TEXT NOT NULL CHECK(stage IN ('url','source')),
 result TEXT NOT NULL CHECK(result IN ('passed','rejected','overridden')), probability REAL NOT NULL CHECK(probability BETWEEN 0 AND 1),
 threshold REAL NOT NULL CHECK(threshold BETWEEN 0 AND 1), contributions TEXT NOT NULL, created_at TEXT NOT NULL,
 override_of INTEGER REFERENCES filter_decisions(id)
);
CREATE TABLE digest_feedback (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id), digest_id INTEGER NOT NULL REFERENCES generated_artifacts(id),
 quality TEXT NOT NULL CHECK(quality IN ('good','inaccurate','incomplete','unclear')), note TEXT NOT NULL, recorded_at TEXT NOT NULL
);
CREATE TABLE interest_predictions (
 digest_id INTEGER PRIMARY KEY REFERENCES generated_artifacts(id), probability REAL NOT NULL CHECK(probability BETWEEN 0 AND 1),
 reason TEXT NOT NULL, profile TEXT NOT NULL, profile_hash TEXT NOT NULL
);
CREATE TABLE digest_checkpoints (
 job_id INTEGER NOT NULL REFERENCES jobs(id), round INTEGER NOT NULL, part INTEGER NOT NULL,
 input_hash TEXT NOT NULL, markdown_path TEXT NOT NULL, hash TEXT NOT NULL,
 actual_model TEXT NOT NULL, provider TEXT NOT NULL, created_at TEXT NOT NULL, input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
 PRIMARY KEY(job_id,round,part)
);
CREATE TABLE embedding_runs (
 snapshot_id INTEGER NOT NULL REFERENCES source_versions(id), version TEXT NOT NULL,
 model_revision TEXT NOT NULL, chunk_bytes INTEGER NOT NULL, dimensions INTEGER NOT NULL,
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','retry_wait','done','failed','cancelled')),
 next_part INTEGER NOT NULL DEFAULT 0, total_parts INTEGER NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0, next_attempt INTEGER NOT NULL DEFAULT 0,
 token TEXT NOT NULL DEFAULT '', lease_until INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
 PRIMARY KEY(snapshot_id,version)
);
CREATE TABLE embedding_chunks (
 snapshot_id INTEGER NOT NULL, version TEXT NOT NULL, part INTEGER NOT NULL,
 start_byte INTEGER NOT NULL, end_byte INTEGER NOT NULL, vector TEXT NOT NULL,
 PRIMARY KEY(snapshot_id,version,part),
 FOREIGN KEY(snapshot_id,version) REFERENCES embedding_runs(snapshot_id,version) ON DELETE CASCADE
);
CREATE TABLE connection_notes (
 id INTEGER PRIMARY KEY, stable_id TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL
);
CREATE TABLE connection_versions (
 id INTEGER PRIMARY KEY, note_id INTEGER NOT NULL REFERENCES connection_notes(id),
 revision INTEGER NOT NULL, title TEXT NOT NULL, category TEXT NOT NULL,
 tags_json TEXT NOT NULL, questions_json TEXT NOT NULL, body TEXT NOT NULL, search_text TEXT NOT NULL,
 markdown_path TEXT NOT NULL, hash TEXT NOT NULL, created_at TEXT NOT NULL, author TEXT NOT NULL DEFAULT 'user', model TEXT NOT NULL DEFAULT '', prompt_version TEXT NOT NULL DEFAULT '',
 UNIQUE(note_id,revision)
);
CREATE TABLE connection_members (
 version_id INTEGER NOT NULL REFERENCES connection_versions(id),
 item_id INTEGER NOT NULL REFERENCES items(id), snapshot_id INTEGER NOT NULL REFERENCES source_versions(id),
 digest_id INTEGER REFERENCES generated_artifacts(id), role TEXT NOT NULL,
 PRIMARY KEY(version_id,item_id)
);
CREATE TABLE job_failures (
 id INTEGER PRIMARY KEY, job_id INTEGER NOT NULL REFERENCES jobs(id), attempt INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('temporary','format','configuration','limit','source','unknown')),
 message TEXT NOT NULL, retryable INTEGER NOT NULL CHECK(retryable IN (0,1)),
 response_path TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL DEFAULT '', recorded_at TEXT NOT NULL
);
CREATE TABLE knowledge_runs (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id),
 snapshot_id INTEGER NOT NULL REFERENCES source_versions(id), digest_id INTEGER NOT NULL REFERENCES generated_artifacts(id),
 model TEXT NOT NULL, prompt_version TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','retry_wait','done','failed','cancelled')),
 attempts INTEGER NOT NULL DEFAULT 0, token TEXT NOT NULL DEFAULT '', lease_until INTEGER NOT NULL DEFAULT 0,
 next_attempt INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '',
 linked_count INTEGER NOT NULL DEFAULT 0, UNIQUE(digest_id,prompt_version,model)
);
CREATE TABLE article_knowledge (
 id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL UNIQUE REFERENCES knowledge_runs(id),
 item_id INTEGER NOT NULL REFERENCES items(id), snapshot_id INTEGER NOT NULL REFERENCES source_versions(id),
 digest_id INTEGER NOT NULL REFERENCES generated_artifacts(id), category TEXT NOT NULL,
 tags_json TEXT NOT NULL, questions_json TEXT NOT NULL, search_text TEXT NOT NULL, markdown_path TEXT NOT NULL, hash TEXT NOT NULL,
 model TEXT NOT NULL, prompt_version TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE knowledge_failures (
 id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL REFERENCES knowledge_runs(id), kind TEXT NOT NULL,
 message TEXT NOT NULL, recorded_at TEXT NOT NULL, response_path TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL DEFAULT ''
);
CREATE TABLE knowledge_batches (
 request_token TEXT PRIMARY KEY, batch_limit INTEGER NOT NULL, include_retry INTEGER NOT NULL,
 created_at TEXT NOT NULL, result_json TEXT NOT NULL
);
CREATE TABLE chat_sessions (
 id TEXT PRIMARY KEY, state_json TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL
);
CREATE TABLE chat_turns (
 id INTEGER PRIMARY KEY, session_id TEXT NOT NULL REFERENCES chat_sessions(id),
 request_token TEXT NOT NULL, request_hash TEXT NOT NULL, user_text TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('planning','applying','done','interrupted')),
 created_at TEXT NOT NULL, deadline INTEGER NOT NULL, response_json TEXT NOT NULL DEFAULT '',
 UNIQUE(session_id,request_token)
);
CREATE TABLE trash_entries (
 item_id INTEGER PRIMARY KEY REFERENCES items(id),
 reason TEXT NOT NULL, moved_at TEXT NOT NULL
);
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
CREATE TABLE completeness_confirmations (
 id INTEGER PRIMARY KEY,
 snapshot_id INTEGER NOT NULL REFERENCES source_versions(id),
 complete INTEGER NOT NULL CHECK(complete IN (0,1)),
 note TEXT NOT NULL DEFAULT '',
 recorded_at TEXT NOT NULL
);
CREATE VIEW active_items AS SELECT * FROM items
 WHERE NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=items.id);
CREATE UNIQUE INDEX one_active_fetch ON jobs(item_id) WHERE stage='fetch' AND state IN ('queued','running','retry_wait');
CREATE UNIQUE INDEX one_digest_job ON jobs(snapshot_id,config_version) WHERE stage='digest';
CREATE INDEX filter_history ON filter_decisions(item_id,id DESC);
CREATE INDEX embedding_version_state ON embedding_runs(version,state,snapshot_id);
CREATE INDEX embedding_chunk_version ON embedding_chunks(version,snapshot_id);
CREATE INDEX connection_backlinks ON connection_members(item_id,version_id);
CREATE INDEX job_failure_history ON job_failures(job_id,id DESC);
CREATE INDEX knowledge_current ON knowledge_runs(item_id,snapshot_id,updated_at DESC,id DESC);
CREATE INDEX article_knowledge_source ON article_knowledge(item_id,snapshot_id);
CREATE INDEX chat_history ON chat_turns(session_id,id DESC);
CREATE INDEX search_judgments_event ON search_judgments(event_id,item_id,id);
CREATE INDEX completeness_confirmations_snapshot ON completeness_confirmations(snapshot_id,id);
CREATE TRIGGER artifact_source_owner BEFORE INSERT ON generated_artifacts
WHEN NOT EXISTS(SELECT 1 FROM source_versions WHERE id=NEW.snapshot_id AND item_id=NEW.item_id)
BEGIN SELECT RAISE(ABORT,'artifact snapshot belongs to another item'); END;
CREATE TRIGGER immutable_snapshot_update BEFORE UPDATE ON source_versions BEGIN SELECT RAISE(ABORT,'immutable snapshot'); END;
CREATE TRIGGER immutable_artifact_update BEFORE UPDATE ON generated_artifacts BEGIN SELECT RAISE(ABORT,'immutable artifact'); END;
CREATE TRIGGER immutable_rules_update BEFORE UPDATE ON filter_rule_versions BEGIN SELECT RAISE(ABORT,'immutable rules'); END;
CREATE TRIGGER immutable_rules_delete BEFORE DELETE ON filter_rule_versions BEGIN SELECT RAISE(ABORT,'immutable rules'); END;
CREATE TRIGGER filter_snapshot_owner BEFORE INSERT ON filter_decisions
WHEN NEW.snapshot_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM source_versions WHERE id=NEW.snapshot_id AND item_id=NEW.item_id)
BEGIN SELECT RAISE(ABORT,'filter snapshot belongs to another item'); END;
CREATE TRIGGER immutable_filter_update BEFORE UPDATE ON filter_decisions BEGIN SELECT RAISE(ABORT,'immutable decision'); END;
CREATE TRIGGER feedback_digest_owner BEFORE INSERT ON digest_feedback
WHEN NOT EXISTS(SELECT 1 FROM generated_artifacts WHERE id=NEW.digest_id AND item_id=NEW.item_id)
BEGIN SELECT RAISE(ABORT,'feedback digest belongs to another item'); END;
CREATE TRIGGER immutable_feedback_update BEFORE UPDATE ON digest_feedback BEGIN SELECT RAISE(ABORT,'immutable feedback'); END;
CREATE TRIGGER immutable_prediction_update BEFORE UPDATE ON interest_predictions BEGIN SELECT RAISE(ABORT,'immutable prediction'); END;
CREATE TRIGGER immutable_checkpoint_update BEFORE UPDATE ON digest_checkpoints BEGIN SELECT RAISE(ABORT,'immutable checkpoint'); END;
CREATE TRIGGER connection_member_owner BEFORE INSERT ON connection_members
WHEN NOT EXISTS(SELECT 1 FROM source_versions WHERE id=NEW.snapshot_id AND item_id=NEW.item_id)
 OR (NEW.digest_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM generated_artifacts WHERE id=NEW.digest_id AND item_id=NEW.item_id AND snapshot_id=NEW.snapshot_id))
BEGIN SELECT RAISE(ABORT,'connection source ownership mismatch'); END;
CREATE TRIGGER immutable_connection_version_update BEFORE UPDATE ON connection_versions BEGIN SELECT RAISE(ABORT,'immutable connection version'); END;
CREATE TRIGGER immutable_connection_version_delete BEFORE DELETE ON connection_versions BEGIN SELECT RAISE(ABORT,'immutable connection version'); END;
CREATE TRIGGER immutable_connection_member_update BEFORE UPDATE ON connection_members BEGIN SELECT RAISE(ABORT,'immutable connection member'); END;
CREATE TRIGGER immutable_connection_member_delete BEFORE DELETE ON connection_members BEGIN SELECT RAISE(ABORT,'immutable connection member'); END;
CREATE TRIGGER immutable_job_failure_update BEFORE UPDATE ON job_failures BEGIN SELECT RAISE(ABORT,'immutable job failure'); END;
CREATE TRIGGER knowledge_run_owner BEFORE INSERT ON knowledge_runs
WHEN NOT EXISTS(SELECT 1 FROM generated_artifacts d WHERE d.id=NEW.digest_id AND d.item_id=NEW.item_id AND d.snapshot_id=NEW.snapshot_id)
BEGIN SELECT RAISE(ABORT,'knowledge source ownership mismatch'); END;
CREATE TRIGGER knowledge_metadata_owner BEFORE INSERT ON article_knowledge
WHEN NOT EXISTS(SELECT 1 FROM knowledge_runs r WHERE r.id=NEW.run_id AND r.item_id=NEW.item_id AND r.snapshot_id=NEW.snapshot_id AND r.digest_id=NEW.digest_id)
BEGIN SELECT RAISE(ABORT,'knowledge metadata ownership mismatch'); END;
CREATE TRIGGER immutable_article_knowledge_update BEFORE UPDATE ON article_knowledge BEGIN SELECT RAISE(ABORT,'immutable knowledge'); END;
CREATE TRIGGER immutable_knowledge_failure_update BEFORE UPDATE ON knowledge_failures BEGIN SELECT RAISE(ABORT,'immutable failure'); END;
CREATE TRIGGER immutable_knowledge_batch_update BEFORE UPDATE ON knowledge_batches BEGIN SELECT RAISE(ABORT,'immutable batch'); END;
CREATE TRIGGER immutable_knowledge_batch_delete BEFORE DELETE ON knowledge_batches BEGIN SELECT RAISE(ABORT,'immutable batch'); END;
CREATE TRIGGER immutable_chat_input BEFORE UPDATE ON chat_turns
WHEN NEW.session_id<>OLD.session_id OR NEW.request_token<>OLD.request_token OR NEW.request_hash<>OLD.request_hash OR NEW.user_text<>OLD.user_text
BEGIN SELECT RAISE(ABORT,'immutable chat input'); END;
CREATE TRIGGER immutable_snapshot_delete BEFORE DELETE ON source_versions
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=OLD.item_id)
 BEGIN SELECT RAISE(ABORT,'immutable snapshot'); END;
CREATE TRIGGER immutable_artifact_delete BEFORE DELETE ON generated_artifacts
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=OLD.item_id)
 BEGIN SELECT RAISE(ABORT,'immutable artifact'); END;
CREATE TRIGGER immutable_filter_delete BEFORE DELETE ON filter_decisions
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=OLD.item_id)
 BEGIN SELECT RAISE(ABORT,'immutable decision'); END;
CREATE TRIGGER immutable_feedback_delete BEFORE DELETE ON digest_feedback
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=OLD.item_id)
 BEGIN SELECT RAISE(ABORT,'immutable feedback'); END;
CREATE TRIGGER immutable_prediction_delete BEFORE DELETE ON interest_predictions
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=(SELECT item_id FROM generated_artifacts WHERE id=OLD.digest_id))
 BEGIN SELECT RAISE(ABORT,'immutable prediction'); END;
CREATE TRIGGER immutable_checkpoint_delete BEFORE DELETE ON digest_checkpoints
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=(SELECT item_id FROM jobs WHERE id=OLD.job_id))
 BEGIN SELECT RAISE(ABORT,'immutable checkpoint'); END;
CREATE TRIGGER immutable_job_failure_delete BEFORE DELETE ON job_failures
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=(SELECT item_id FROM jobs WHERE id=OLD.job_id))
 BEGIN SELECT RAISE(ABORT,'immutable job failure'); END;
CREATE TRIGGER immutable_article_knowledge_delete BEFORE DELETE ON article_knowledge
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=OLD.item_id)
 BEGIN SELECT RAISE(ABORT,'immutable knowledge'); END;
CREATE TRIGGER immutable_knowledge_failure_delete BEFORE DELETE ON knowledge_failures
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=(SELECT item_id FROM knowledge_runs WHERE id=OLD.run_id))
 BEGIN SELECT RAISE(ABORT,'immutable failure'); END;
PRAGMA user_version=1;
