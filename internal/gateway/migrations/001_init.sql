CREATE TABLE
	users (
		id TEXT NOT NULL PRIMARY KEY,
		enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
		current_policy_id TEXT NOT NULL,
		created INTEGER NOT NULL,
		model_access_group TEXT NOT NULL CHECK (length (model_access_group) > 0),
		FOREIGN KEY (id, current_policy_id) REFERENCES budget_policies (user_id, id) DEFERRABLE INITIALLY DEFERRED
	);

CREATE TABLE
	budget_allowances (
		id TEXT NOT NULL PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users (id),
		cadence TEXT NOT NULL CHECK (cadence IN ('weekly', 'monthly')),
		anchor INTEGER NOT NULL,
		UNIQUE (user_id, id)
	);

CREATE TRIGGER immutable_budget_allowance_update BEFORE
UPDATE ON budget_allowances BEGIN
SELECT
	RAISE (ABORT, 'immutable budget allowance');

END;

CREATE TRIGGER immutable_budget_allowance_delete BEFORE DELETE ON budget_allowances BEGIN
SELECT
	RAISE (ABORT, 'immutable budget allowance');

END;

CREATE TABLE
	budget_policies (
		id TEXT NOT NULL PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users (id),
		budget INTEGER NOT NULL CHECK (budget >= 0),
		created INTEGER NOT NULL,
		allowance_id TEXT NOT NULL,
		UNIQUE (user_id, id),
		UNIQUE (user_id, id, allowance_id),
		FOREIGN KEY (user_id, allowance_id) REFERENCES budget_allowances (user_id, id)
	);

CREATE TRIGGER immutable_budget_policy_identity BEFORE
UPDATE OF id,
user_id,
created,
allowance_id ON budget_policies BEGIN
SELECT
	RAISE (ABORT, 'immutable budget policy identity');

END;

CREATE TRIGGER immutable_budget_policy_delete BEFORE DELETE ON budget_policies BEGIN
SELECT
	RAISE (ABORT, 'immutable budget policy');

END;

CREATE TABLE
	budget_transitions (
		user_id TEXT NOT NULL REFERENCES users (id),
		id TEXT NOT NULL,
		policy_id TEXT NOT NULL,
		input TEXT NOT NULL,
		result TEXT NOT NULL,
		PRIMARY KEY (user_id, id),
		FOREIGN KEY (user_id, policy_id) REFERENCES budget_policies (user_id, id)
	);

CREATE TRIGGER immutable_budget_transition_update BEFORE
UPDATE ON budget_transitions BEGIN
SELECT
	RAISE (ABORT, 'immutable budget transition');

END;

CREATE TRIGGER immutable_budget_transition_delete BEFORE DELETE ON budget_transitions BEGIN
SELECT
	RAISE (ABORT, 'immutable budget transition');

END;

CREATE TABLE
	virtual_keys (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users (id),
		hash TEXT NOT NULL UNIQUE,
		alias TEXT NOT NULL DEFAULT '',
		prefix TEXT NOT NULL,
		expires INTEGER,
		revoked INTEGER NOT NULL DEFAULT 0 CHECK (revoked IN (0, 1)),
		created INTEGER NOT NULL,
		is_current INTEGER NOT NULL DEFAULT 1 CHECK (is_current IN (0, 1)),
		rotated INTEGER
	);

CREATE INDEX keys_user ON virtual_keys (user_id);

CREATE UNIQUE INDEX keys_alias_current ON virtual_keys (user_id, alias)
WHERE
	is_current = 1;

CREATE INDEX keys_user_current ON virtual_keys (user_id, is_current, id);

CREATE TABLE
	periods (
		id TEXT NOT NULL PRIMARY KEY,
		user_id TEXT NOT NULL,
		allowance_id TEXT NOT NULL,
		start INTEGER NOT NULL,
		end INTEGER NOT NULL CHECK (end > start),
		spent INTEGER NOT NULL DEFAULT 0 CHECK (spent >= 0),
		UNIQUE (allowance_id, start),
		UNIQUE (user_id, id, start, allowance_id),
		FOREIGN KEY (user_id, allowance_id) REFERENCES budget_allowances (user_id, id)
	);

CREATE INDEX periods_user_history ON periods (user_id, start DESC, id DESC);

CREATE TRIGGER immutable_period_identity BEFORE
UPDATE OF id,
user_id,
allowance_id,
start,
end ON periods BEGIN
SELECT
	RAISE (ABORT, 'immutable period identity');

END;

CREATE TABLE
	requests (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users (id),
		key_id TEXT NOT NULL REFERENCES virtual_keys (id),
		model_access_group TEXT NOT NULL,
		model TEXT NOT NULL,
		provider TEXT NOT NULL,
		upstream_model TEXT NOT NULL,
		route TEXT NOT NULL,
		provider_config TEXT NOT NULL DEFAULT '{}',
		generation_id TEXT NOT NULL DEFAULT '',
		created INTEGER NOT NULL,
		period_start INTEGER NOT NULL,
		period_id TEXT NOT NULL,
		policy_id TEXT NOT NULL,
		allowance_id TEXT NOT NULL,
		lifecycle TEXT NOT NULL DEFAULT 'admitted',
		accounting TEXT NOT NULL DEFAULT 'pending',
		cost INTEGER,
		raw_cost TEXT NOT NULL DEFAULT '',
		usage TEXT NOT NULL DEFAULT '{}',
		attempts INTEGER NOT NULL DEFAULT 0,
		next_attempt INTEGER NOT NULL DEFAULT 0,
		error_code TEXT NOT NULL DEFAULT '',
		finished INTEGER,
		FOREIGN KEY (user_id, period_id, period_start, allowance_id) REFERENCES periods (user_id, id, start, allowance_id),
		FOREIGN KEY (user_id, policy_id, allowance_id) REFERENCES budget_policies (user_id, id, allowance_id)
	);

CREATE TRIGGER immutable_request_period BEFORE
UPDATE OF user_id,
period_id,
period_start,
policy_id,
allowance_id ON requests BEGIN
SELECT
	RAISE (ABORT, 'immutable request period');

END;

CREATE INDEX requests_user_history ON requests (user_id, created DESC, id DESC);

CREATE INDEX requests_accounting ON requests (accounting, next_attempt, created);

CREATE TABLE
	ledger (
		id INTEGER PRIMARY KEY,
		request_id TEXT NOT NULL REFERENCES requests (id),
		amount INTEGER NOT NULL,
		source TEXT NOT NULL,
		reason TEXT NOT NULL,
		created INTEGER NOT NULL
	);

CREATE UNIQUE INDEX ledger_initial ON ledger (request_id)
WHERE
	source != 'manual_adjustment';

CREATE TABLE
	audit (
		id INTEGER PRIMARY KEY,
		action TEXT NOT NULL,
		target TEXT NOT NULL,
		detail TEXT NOT NULL,
		created INTEGER NOT NULL
	);