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
CREATE INDEX embedding_version_state ON embedding_runs(version,state,snapshot_id);
CREATE INDEX embedding_chunk_version ON embedding_chunks(version,snapshot_id);
PRAGMA user_version=7;
