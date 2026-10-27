CREATE TABLE users (
 id TEXT NOT NULL PRIMARY KEY, enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
 budget INTEGER NOT NULL CHECK(budget>=0), cadence TEXT NOT NULL CHECK(cadence IN ('weekly','monthly')),
 anchor INTEGER NOT NULL, created INTEGER NOT NULL,
 model_access_group TEXT NOT NULL CHECK(length(model_access_group)>0)
);
CREATE TABLE virtual_keys (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), hash TEXT NOT NULL UNIQUE,
 alias TEXT NOT NULL DEFAULT '', prefix TEXT NOT NULL, expires INTEGER,
 revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN (0,1)), created INTEGER NOT NULL,
 is_current INTEGER NOT NULL DEFAULT 1 CHECK(is_current IN (0,1)), rotated INTEGER
);
CREATE INDEX keys_user ON virtual_keys(user_id);
-- Revoked aliases remain reserved; retired credentials retain their aliases and
-- accounting links without becoming a second current credential.
CREATE UNIQUE INDEX keys_alias_current ON virtual_keys(user_id,alias) WHERE is_current=1;
CREATE INDEX keys_user_current ON virtual_keys(user_id,is_current,id);
CREATE TABLE periods (
 user_id TEXT NOT NULL REFERENCES users(id), start INTEGER NOT NULL, end INTEGER NOT NULL,
 spent INTEGER NOT NULL DEFAULT 0 CHECK(spent>=0), PRIMARY KEY(user_id,start)
);
CREATE TABLE requests (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), key_id TEXT NOT NULL REFERENCES virtual_keys(id),
 model_access_group TEXT NOT NULL, model TEXT NOT NULL, provider TEXT NOT NULL, upstream_model TEXT NOT NULL, route TEXT NOT NULL,
 provider_config TEXT NOT NULL DEFAULT '{}',
 generation_id TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL, period_start INTEGER NOT NULL,
 lifecycle TEXT NOT NULL DEFAULT 'admitted', accounting TEXT NOT NULL DEFAULT 'pending',
 cost INTEGER, raw_cost TEXT NOT NULL DEFAULT '', usage TEXT NOT NULL DEFAULT '{}',
 attempts INTEGER NOT NULL DEFAULT 0, next_attempt INTEGER NOT NULL DEFAULT 0,
 error_code TEXT NOT NULL DEFAULT '', finished INTEGER,
 FOREIGN KEY(user_id,period_start) REFERENCES periods(user_id,start)
);
CREATE INDEX requests_user_history ON requests(user_id,created DESC,id DESC);
CREATE INDEX requests_accounting ON requests(accounting,next_attempt,created);
CREATE TABLE ledger (
 id INTEGER PRIMARY KEY, request_id TEXT NOT NULL REFERENCES requests(id), amount INTEGER NOT NULL,
 source TEXT NOT NULL, reason TEXT NOT NULL, created INTEGER NOT NULL
);
CREATE UNIQUE INDEX ledger_initial ON ledger(request_id) WHERE source != 'manual_adjustment';
CREATE TABLE audit (
 id INTEGER PRIMARY KEY, action TEXT NOT NULL, target TEXT NOT NULL, detail TEXT NOT NULL, created INTEGER NOT NULL
);
