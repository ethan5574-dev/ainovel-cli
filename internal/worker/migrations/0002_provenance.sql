-- Phase 3: dữ liệu "hồ sơ sản xuất" (provenance) — điểm review 7 chiều của Editor và
-- sổ cái phục bút/伏笔, cả hai đã có sẵn trong engine (internal/domain/review.go), chỉ
-- cần harvest sang Postgres để trang provenance của Next.js đọc mà không cần đụng
-- filesystem của book directory. Cũng thêm cổng xuất bản: một cuốn chỉ public khi
-- published_at khác NULL, do worker set sau khi tính quality gate.

ALTER TABLE novels ADD COLUMN IF NOT EXISTS quality_score NUMERIC(5, 2);
ALTER TABLE novels ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS chapter_reviews (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    novel_id    UUID NOT NULL REFERENCES novels(id),
    chapter     INT NOT NULL,
    scope       TEXT NOT NULL, -- chapter | arc | global (domain.ReviewEntry.Scope)
    verdict     TEXT NOT NULL DEFAULT '', -- accept | polish | rewrite
    summary     TEXT NOT NULL DEFAULT '',
    -- Mảng [{dimension, score, comment}], đúng 7 chiều rubric hiện tại của Editor
    -- (consistency/character/pacing/continuity/foreshadow/hook/aesthetic) — nhưng
    -- lưu nguyên dạng JSON thay vì cột riêng từng chiều, vì rubric có thể mở rộng
    -- (assets/prompts/editor.md sở hữu danh sách chiều, không phải schema này).
    dimensions  JSONB NOT NULL DEFAULT '[]',
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (novel_id, chapter, scope)
);

CREATE TABLE IF NOT EXISTS foreshadows (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    novel_id      UUID NOT NULL REFERENCES novels(id),
    foreshadow_id TEXT NOT NULL, -- domain.ForeshadowEntry.ID, ổn định trong suốt 1 cuốn
    description   TEXT NOT NULL DEFAULT '',
    planted_at    INT NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT '', -- planted | advanced | resolved
    resolved_at   INT,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (novel_id, foreshadow_id)
);

CREATE INDEX IF NOT EXISTS idx_chapter_reviews_novel ON chapter_reviews (novel_id, chapter);
CREATE INDEX IF NOT EXISTS idx_foreshadows_novel ON foreshadows (novel_id);
