-- Schema cho nền tảng (Phase 1). Sinh UUID phía Postgres nên worker không cần
-- thư viện UUID riêng. Xem plan: users/novels/chapters/generation_jobs/usage_ledger.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- name/image là 2 cột duy nhất tồn tại vì Auth.js's DrizzleAdapter (nền tảng Next.js)
-- đòi hỏi đúng shape này trên bảng users nó dùng để đăng nhập; worker Go không đọc/ghi
-- 2 cột này. users được dùng chung giữa hai hệ thống — cố tình không tách 2 bảng
-- users để tránh 2 bản ghi định danh lệch nhau cho cùng một người.
CREATE TABLE IF NOT EXISTS users (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email             TEXT NOT NULL UNIQUE,
    email_verified_at TIMESTAMPTZ,
    name              TEXT,
    image             TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS novels (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id),
    title         TEXT NOT NULL DEFAULT '',
    prompt        TEXT NOT NULL,
    -- draft: chưa sinh xong / generating: đang chạy / complete: sinh xong /
    -- failed: dừng vì lỗi hoặc chạm hạn mức. Trạng thái publish (Phase 3) là cột riêng, tách khỏi vòng đời sinh.
    status        TEXT NOT NULL DEFAULT 'draft',
    workspace_dir TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS chapters (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    novel_id     UUID NOT NULL REFERENCES novels(id),
    number       INT NOT NULL,
    title        TEXT NOT NULL DEFAULT '',
    storage_key  TEXT NOT NULL,
    word_count   INT NOT NULL DEFAULT 0,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (novel_id, number)
);

CREATE TABLE IF NOT EXISTS generation_jobs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    novel_id     UUID NOT NULL REFERENCES novels(id),
    -- queued -> running -> (done | failed). RequeueStale đưa running quá hạn heartbeat về lại queued.
    status       TEXT NOT NULL DEFAULT 'queued',
    tier         TEXT NOT NULL DEFAULT 'trial', -- trial | free | paid — quyết định model/budget khi claim
    budget_usd   NUMERIC(10, 4) NOT NULL,
    max_chapters INT,
    claimed_by   TEXT,
    claimed_at   TIMESTAMPTZ,
    heartbeat_at TIMESTAMPTZ,
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Hàng đợi chỉ quét status='queued'; partial index giữ nó nhỏ khi job tích luỹ theo thời gian.
CREATE INDEX IF NOT EXISTS idx_generation_jobs_queued
    ON generation_jobs (created_at)
    WHERE status = 'queued';

-- RequeueStale quét running theo heartbeat_at cũ.
CREATE INDEX IF NOT EXISTS idx_generation_jobs_running_heartbeat
    ON generation_jobs (heartbeat_at)
    WHERE status = 'running';

CREATE TABLE IF NOT EXISTS usage_ledger (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id            UUID NOT NULL REFERENCES generation_jobs(id),
    novel_id          UUID NOT NULL REFERENCES novels(id),
    input_tokens      BIGINT NOT NULL DEFAULT 0,
    output_tokens     BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    cost_usd          NUMERIC(10, 6) NOT NULL DEFAULT 0,
    recorded_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Hạn mức ngày toàn hệ thống (lớp chặn chi phí số 3 trong plan) đọc bằng
-- SUM(cost_usd) WHERE recorded_at >= <đầu ngày>; index này giữ truy vấn đó rẻ
-- mà không cần thêm bảng đếm riêng.
CREATE INDEX IF NOT EXISTS idx_usage_ledger_recorded_at ON usage_ledger (recorded_at);
