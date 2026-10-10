ALTER TABLE jobs ADD COLUMN chunk_limit INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN chunk_max_parts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE generated_artifacts ADD COLUMN chunk_count INTEGER NOT NULL DEFAULT 1;
ALTER TABLE cache_entries ADD COLUMN last_accessed INTEGER NOT NULL DEFAULT 0;
CREATE TABLE digest_checkpoints (
 job_id INTEGER NOT NULL REFERENCES jobs(id), round INTEGER NOT NULL, part INTEGER NOT NULL,
 input_hash TEXT NOT NULL, markdown_path TEXT NOT NULL, hash TEXT NOT NULL,
 actual_model TEXT NOT NULL, provider TEXT NOT NULL, created_at TEXT NOT NULL, input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
 PRIMARY KEY(job_id,round,part)
);
CREATE TRIGGER immutable_checkpoint_update BEFORE UPDATE ON digest_checkpoints BEGIN SELECT RAISE(ABORT,'immutable checkpoint'); END;
CREATE TRIGGER immutable_checkpoint_delete BEFORE DELETE ON digest_checkpoints BEGIN SELECT RAISE(ABORT,'immutable checkpoint'); END;
PRAGMA user_version=5;
