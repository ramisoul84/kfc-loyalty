CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- A loyalty member is a customer identified by phone number.
-- points_balance is maintained in the same transaction as every ledger
-- insert (with the member row locked), so it always equals the ledger sum.
CREATE TABLE IF NOT EXISTS members (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone           VARCHAR(20)  NOT NULL UNIQUE,
    first_name      VARCHAR(60),
    email           VARCHAR(255),
    points_balance  INT          NOT NULL DEFAULT 0,
    lifetime_points INT          NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_members_created ON members (created_at DESC);

-- Append-only points ledger. Never updated or deleted.
CREATE TABLE IF NOT EXISTS ledger (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    member_id     UUID         NOT NULL REFERENCES members(id),
    type          VARCHAR(20)  NOT NULL,
    points        INT          NOT NULL,
    balance_after INT          NOT NULL,
    order_id      UUID,
    order_number  INT,
    restaurant_id UUID,
    hold_id       UUID,
    description   VARCHAR(200),
    actor         VARCHAR(100),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_ledger_type CHECK (type IN ('welcome', 'earn', 'redeem', 'refund', 'adjust')),
    CONSTRAINT chk_ledger_points CHECK (points <> 0)
);

CREATE INDEX IF NOT EXISTS idx_ledger_member ON ledger (member_id, created_at DESC);

-- One earn, one redeem and one refund per order: replayed Kafka events and
-- retried calls become no-ops instead of double-counting.
CREATE UNIQUE INDEX IF NOT EXISTS uq_ledger_order_type
    ON ledger (order_id, type)
    WHERE order_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_ledger_welcome
    ON ledger (member_id)
    WHERE type = 'welcome';

-- Points set aside during checkout. "held" holds count against the
-- available balance until they expire; commit turns them into a redeem
-- ledger entry.
CREATE TABLE IF NOT EXISTS holds (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    member_id  UUID         NOT NULL REFERENCES members(id),
    points     INT          NOT NULL CHECK (points > 0),
    reference  VARCHAR(100) NOT NULL,
    status     VARCHAR(20)  NOT NULL DEFAULT 'held',
    order_id   UUID,
    expires_at TIMESTAMPTZ  NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_holds_status CHECK (status IN ('held', 'committed', 'released')),
    CONSTRAINT uq_holds_reference UNIQUE (member_id, reference)
);

CREATE INDEX IF NOT EXISTS idx_holds_active
    ON holds (member_id, expires_at)
    WHERE status = 'held';

-- Single-use kiosk identification codes. Only a SHA-256 of the code is
-- stored; a code lives for minutes and one member has at most one.
CREATE TABLE IF NOT EXISTS member_codes (
    code_hash  CHAR(64)    PRIMARY KEY,
    member_id  UUID        NOT NULL REFERENCES members(id),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_member_codes_member ON member_codes (member_id);
