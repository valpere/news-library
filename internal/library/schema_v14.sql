ALTER TABLE rating_events ADD COLUMN scale TEXT NOT NULL DEFAULT 'legacy_numeric'
 CHECK(scale IN ('legacy_numeric','estimative_usefulness_v1') AND (scale='legacy_numeric' OR score BETWEEN 0 AND 6));
PRAGMA user_version=14;
