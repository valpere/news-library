CREATE TABLE completeness_confirmations (
 id INTEGER PRIMARY KEY,
 snapshot_id INTEGER NOT NULL REFERENCES source_versions(id),
 complete INTEGER NOT NULL CHECK(complete IN (0,1)),
 note TEXT NOT NULL DEFAULT '',
 recorded_at TEXT NOT NULL
);
CREATE INDEX completeness_confirmations_snapshot ON completeness_confirmations(snapshot_id,id);
PRAGMA user_version=17;
