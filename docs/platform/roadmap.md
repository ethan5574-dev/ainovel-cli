# Phân kỳ và trạng thái

Cập nhật lần cuối: 2026-08-08. Đây là nguồn trạng thái mới nhất — nếu lịch sử hội thoại
nói khác, tài liệu này thắng.

## Phase 0 — Đo giá/chất lượng thật (chưa làm, hoãn theo yêu cầu)

**Mục tiêu:** đây là bước khử rủi ro quan trọng nhất; toàn bộ business model đứng hay
sụp ở kết quả này.

- Chạy local ~10 chương trên 3 model: Gemini 2.5 Flash-Lite (ứng viên chính cho tier
  miễn phí), DeepSeek V3 (đối chứng), Gemini 2.5 Flash (mốc tham chiếu chất lượng — để
  biết model rẻ kém hơn bao nhiêu, tức biết tier trả phí sau này bán được gì). Tổng chi
  phí dự kiến ~$3.
- Đọc `meta/usage.json` → $/chương thật, thay ước tính. Đối chiếu ít nhất một lần với
  dashboard billing thật của nhà cung cấp.
- Đánh giá chất lượng tiếng Việt — engine vốn thiên tiếng Trung (prompt gốc tiếng
  Trung, ước lượng token kiểu CJK, tách chương theo định dạng Trung/Anh).
- **Bộ đo đã chuẩn bị sẵn**, chỉ chưa chạy: case tiếng Việt tại `evals/cases/vi/`
  (`architect_short_vi.json`, `writer_chapters_vi.json`), config mẫu 3 model tại
  `.ainovel/eval-configs/` (đã gitignore, chỉ chứa placeholder key), hướng dẫn chạy đầy
  đủ tại `.ainovel/eval-configs/README.md`.
- **Done khi:** có bảng $/chương thật + kết luận model rẻ có dùng được cho tiếng Việt
  không. Nếu không đạt → dừng lại, tính lại giá, đừng xây tiếp.

**Trạng thái:** hoãn theo yêu cầu người dùng ("vấn đề key này tôi có thể giải quyết sau
... cứ mặc định là có key"). Phase 1 và 2 được xây tiếp trên số liệu **ước tính**, chưa
được đo thật xác nhận — xem mục Rủi ro bên dưới.

## Phase 1 — Worker + schema (repo Go + Postgres)

**Trạng thái: code xong, test qua Postgres thật, chưa chạy với key LLM thật.**

Chi tiết kỹ thuật đầy đủ ở [architecture.md](./architecture.md). Tóm tắt:

- `cmd/ainovel-worker/main.go` + `internal/worker/` — poll job, claim bằng
  `FOR UPDATE SKIP LOCKED`, chạy engine qua `host.New()`, harvest tiến độ/usage vào
  Postgres, hạn mức cứng qua `BudgetConfig` + `job.MaxChapters`.
- Schema `internal/worker/migrations/0001_init.sql` — users/novels/chapters/
  generation_jobs/usage_ledger, tự áp dụng khi worker khởi động.
- `docker-compose.yml` profile `platform` (postgres + ainovel-worker), `Dockerfile`
  build thêm binary `ainovel-worker`.

**Test đã chạy thật, không chỉ biên dịch:**
- `internal/worker/harvest_test.go` — progress-diff, đẩy chương, đẩy usage, trên
  `store.NewStore` thật ở `t.TempDir()`.
- `internal/worker/pgstore_test.go` — chạy thật trên Postgres 16 qua Docker: migrate
  idempotent, claim/heartbeat/complete/fail, **8 worker đồng thời claim 8 job không đụng
  nhau** (test trực tiếp tính đúng đắn của `FOR UPDATE SKIP LOCKED`), requeue-stale,
  usage/chapter upsert. Gated bằng biến môi trường `WORKER_TEST_DSN` nên `go test ./...`
  ở máy không có Postgres vẫn sạch (skip, không fail).

**Chưa kiểm chứng** (đúng phần "Done" gốc của Phase 1, cần key LLM thật): chạy `RunJob`
thật hết một cuốn, và kịch bản giết worker giữa chừng rồi bật lại tự chạy tiếp qua
`Resume()`. Cơ chế đã có (poll lại thấy job `queued` sau `RequeueStale`, `RunJob` luôn
thử `eng.Resume()` trước khi `StartPrepared()`) nhưng chưa chạy đầu-cuối với LLM thật.

**Done khi:** submit một job bằng SQL thủ công → worker sinh xong cuốn truyện, chi phí
ghi đúng vào `usage_ledger`, giết worker giữa chừng rồi bật lại vẫn chạy tiếp. **Còn
treo, chờ key thật.**

## Phase 2 — Web app tối thiểu (Next.js)

**Trạng thái: scaffold xong ở repo mới `../ainovel-platform`, build/lint/typecheck
sạch, chưa deploy, chưa chạy thật với SMTP/key thật.**

- Stack: Next.js 16 (App Router, Turbopack) + TypeScript + Tailwind + Drizzle ORM +
  Auth.js (NextAuth v5 beta), đăng nhập bằng magic link qua email — không mật khẩu,
  không API key riêng.
- Trang đã có: `/` (landing), `/signin`, `/dashboard` (danh sách truyện của mình),
  `/novels/new` (form tạo truyện, ghi vào hàng đợi), `/novels/[id]` (đọc tiến độ +
  nội dung chương).
- Hạn mức 1 lần dùng thử/tài khoản: `createNovel` tự kiểm tra tài khoản đã có job
  `tier='trial'` chưa; nếu có, job mới rơi vào tier `free` (`src/lib/tiers.ts`).
- Nhãn AI bắt buộc (`AiDisclosureBanner`) đã gắn ở trang tạo truyện và trang đọc truyện.
- Ranh giới sở hữu schema với worker Go — xem chi tiết đầy đủ (kể cả cạm bẫy
  `drizzle-kit generate` và cách giải quyết) tại [architecture.md](./architecture.md).

**Verify đã chạy thật:** `tsc --noEmit` sạch, `npm run lint` sạch, `npm run build`
(production, Turbopack) sạch. Trong quá trình đó phát hiện và sửa 2 lỗi build thật:
(1) `NextAuth()`/`Nodemailer` validate config ngay lúc module load, nên build **cần**
`EMAIL_SERVER`/`EMAIL_FROM`/`AUTH_SECRET` đã set từ trước, không chỉ lúc chạy — đã ghi
rõ trong README của repo đó; (2) đọc file chương qua `CHAPTER_ROOT` (biến môi trường, có
thể trỏ ngoài project) khiến Turbopack cảnh báo trace toàn bộ project — đã thêm
`turbopackIgnore` đúng chỗ.

**Rủi ro bảo mật đã biết, chưa chạm tới:** `nodemailer` (kéo theo bởi `next-auth`) có
CVE mức cao chưa có bản vá (GHSA-p6gq-j5cr-w38f — đọc file/SSRF qua option `raw` của
message). Code hiện tại không expose option đó cho input người dùng nên chưa khai thác
được, nhưng cần kiểm tra lại nếu sau này có code path nào đưa input người dùng thẳng vào
nodemailer.

**Hạn mức ngày toàn hệ thống (lớp chặn chi phí số 3) — đã code, ở phía worker Go:**
`PGStore.TodaySpendUSD()` (`internal/worker/pgstore.go`) tính `SUM(cost_usd)` từ
`usage_ledger` kể từ `date_trunc('day', now())` (theo giờ của Postgres, không phải giờ
máy worker — để nhiều worker instance đồng thuận cùng một ranh giới ngày). Vòng poll
(`cmd/ainovel-worker/main.go`'s `run()`) kiểm tra trước mỗi lần `ClaimNextJob`: đạt
`DAILY_BUDGET_USD` thì bỏ qua lượt claim đó — job vẫn nằm `queued`, tự chạy lại khi chi
tiêu hôm sau giảm xuống dưới hạn mức, không phải một job cron reset riêng. Mặc định
`DAILY_BUDGET_USD=0` (không giới hạn) nếu không set; `docker-compose.yml` đặt sẵn `"20"`
làm ví dụ cho profile `platform`. Test qua Postgres thật
(`TestPGStore_TodaySpendUSD`, `internal/worker/pgstore_test.go`) — xác nhận cả tổng đúng
lẫn dòng ghi "hôm qua" (`recorded_at` lùi 1 ngày) không bị tính vào.

**Done khi:** người lạ đăng ký từ trình duyệt, tạo được truyện, đọc được, không cài gì
và không nhập key. **Chưa đạt** — cần deploy thật + SMTP thật + key LLM thật để test
đầu-cuối.

## Phase 3 — Xuất bản công khai + trang "hồ sơ sản xuất"

**Trạng thái: code xong cả 2 phía (worker Go + Next.js), verify thật qua Postgres +
dev server. Chưa deploy, chưa có review thật (cần key LLM thật để có dữ liệu, xem Phase 1).**

- **Schema** (`internal/worker/migrations/0002_provenance.sql`, tự áp dụng, idempotent
  qua `schema_migrations` giống 0001): `novels.quality_score`, `novels.published_at`
  (NULL = chưa public — đây là cổng thật sự), bảng mới `chapter_reviews` (điểm 7 chiều,
  lưu dạng JSONB vì rubric có thể mở rộng — schema này không sở hữu danh sách chiều,
  `assets/prompts/editor.md` mới sở hữu) và `foreshadows` (sổ cái phục bút/伏笔).
- **Harvest** (`internal/worker/harvest.go`): `harvestReviews`/`harvestForeshadows` đọc
  qua `WorldStore.LoadAllReviews()` (hàm mới thêm ở `internal/store/world.go`, trả về
  toàn bộ review chapter/arc/global — khác `LoadReviewsAffectingChapter` chỉ trả tập con
  ảnh hưởng 1 chương) và `WorldStore.LoadForeshadowLedger()` (đã có sẵn từ trước), upsert
  vào Postgres. Chạy định kỳ trong `consumeJob` (cùng nhịp với harvest chương) và một lượt
  cuối trong `finishJob`.
- **Cổng chất lượng** (`computeQualityGate` trong harvest.go): trung bình điểm tất cả
  `Dimensions[].Score` của các review `scope=="chapter"`, so với ngưỡng
  `diag.ThresholdDimScoreLow = 70` — mượn nguyên ngưỡng đã có sẵn của engine (cảnh báo
  "ChronicLowDimension") thay vì bịa số mới. Đạt ngưỡng → `SetQualityGate(..., publish:
  true)` stamp `published_at`; không có review nào (`count == 0`) cũng không publish.
  Chỉ chạy khi job **thành công** (`finishJob`'s `succeeded` branch) — review dở dang của
  một job lỗi/hết ngân sách không phải tín hiệu đáng tin để quyết định public hay không.
- **Test đã chạy thật:** `internal/worker/harvest_test.go` (harvest + cổng chất lượng,
  trên `store.NewStore` thật ở `t.TempDir()`, gồm cả case dưới ngưỡng và case 0 review) và
  `internal/worker/pgstore_test.go` (3 test mới: upsert `chapter_reviews`, upsert
  `foreshadows`, `SetQualityGate` cả 2 nhánh publish/không — chạy thật trên Postgres 16
  qua Docker, cùng cơ chế gate `WORKER_TEST_DSN` như Phase 1).
- **Next.js (`../ainovel-platform`):**
  - `src/db/schema.ts` mirror thêm `novels.qualityScore`/`novels.publishedAt` và 2 bảng
    mới `chapterReviews`/`foreshadows` — cùng nguyên tắc Phase 2: chỉ mirror để query,
    không chạy `drizzle-kit generate/push` lên các bảng/cột này (worker sở hữu).
  - `src/app/read/[id]/page.tsx` — trang public, SSR, không cần đăng nhập, có
    `generateMetadata` cho SEO (title/description theo `novel.prompt`). Gate bằng
    `WHERE published_at IS NOT NULL`, không phải `status` — một cuốn `status='complete'`
    vẫn có thể chưa qua cổng chất lượng và phải vô hình ở đây.
  - `src/app/read/[id]/provenance/page.tsx` — outline review 7 chiều theo từng chương,
    review tổng (arc/global), sổ cái phục bút — cùng gate `published_at`.
  - `/novels/[id]` (trang tác giả, cần đăng nhập) được thêm link sang `/read/[id]` khi đã
    publish; chưa publish thì hiện dòng nhắc "chưa qua cổng chất lượng".
  - `src/lib/chapterText.ts` — tách hàm đọc file chương dùng chung giữa `/novels/[id]` và
    `/read/[id]` (trước đó code trùng lặp ở 1 chỗ, tách khi dùng lần 2).
- **Verify đã chạy thật, không chỉ biên dịch:** `tsc --noEmit` sạch, `npm run lint` sạch,
  `npm run build` sạch (kèm 2 route mới `/read/[id]` và `/read/[id]/provenance` xuất hiện
  đúng trong output). Kiểm chứng đầu-cuối qua Postgres 16 thật (Docker): áp migration Go
  (0001+0002) rồi áp migration Next.js (drizzle-kit migrate) lên cùng 1 DB, insert dữ liệu
  qua Drizzle client (novel + chapter_review với `dimensions` JSONB + foreshadow), chạy
  `next dev` trỏ vào DB đó, `curl` cả 2 route: dữ liệu render đúng (điểm dimension, nhãn
  trạng thái phục bút tiếng Việt), và một novel ID không tồn tại/chưa publish trả về 404
  đúng như thiết kế cổng chất lượng.
- **Done khi:** một cuốn tự seed được Google index và người ngoài đọc được (không cần
  đăng nhập). **Chưa đạt** — cần deploy thật + chạy hết một cuốn thật (key LLM thật, xem
  Phase 1) để có review thật đi qua cổng chất lượng, rồi mới có gì để Google index.

## Phase 4 — Lớp cộng đồng

**Trạng thái: code xong ở `../ainovel-platform`, verify thật qua Postgres + dev server.
Chưa deploy.**

Bình luận, theo dõi (follow), bảng xếp hạng, thư viện/danh mục. Theo chuẩn Royal Road
(market-research.md #2): **bình luận/diễn đàn cấm AI** — giữ "tiếng người" ở không gian
tương tác, chỉ nội dung truyện mới được phép có AI kèm nhãn. Không có bộ lọc AI đáng tin
cậy về mặt kỹ thuật, nên đây là quy tắc chính sách (thông báo cạnh ô nhập bình luận),
không phải kiểm tra phía server — giống cách Royal Road thực thi.

- **Schema mới** (`src/db/community-schema.ts`, khác `schema.ts`: bảng này do chính
  Next.js sở hữu, không mirror worker) — `comments` (novel_id, user_id, chapter nullable
  = bình luận cho cả truyện, body) và `follows` (khóa chính kép user_id+novel_id).
  `drizzle.config.ts`'s `schema` chuyển từ 1 file thành mảng
  `[auth-schema.ts, community-schema.ts]`; `drizzle-kit generate` lần này **không** sinh
  `CREATE TABLE` thừa cho `users`/`novels` (khác lần đụng độ ở Phase 2) vì cả hai file chỉ
  import 2 bảng đó để khai FK, không `export` — đã kiểm chứng bằng cách đọc trực tiếp file
  migration sinh ra trước khi áp dụng.
- **`/read/[id]`** — thêm nút theo dõi (server action `toggleFollow`, cần đăng nhập), số
  người theo dõi, khu bình luận (list + form, server action `postComment`) với dòng nhắc
  chính sách "không đăng nội dung do AI viết". Người chưa đăng nhập thấy link đăng nhập
  thay vì form.
- **`/library`** — duyệt toàn bộ truyện đã `published_at IS NOT NULL`, mới nhất trước.
- **`/leaderboard`** — xếp hạng theo số người theo dõi (tín hiệu gắn bó thật của độc giả),
  không theo `quality_score` (đó là cổng đạt/không đạt, không phải thước đo xếp hạng).
- **Cả 2 trang `/library` và `/leaderboard` đặt `export const dynamic = "force-dynamic"`**
  — phát hiện thật khi build: không có, Next.js prerender tĩnh 1 lần lúc build (route
  hiện `○` thay vì `ƒ` trong output `next build`) vì không gọi API động nào, khiến truyện
  mới/theo dõi mới không bao giờ xuất hiện cho tới lần build sau. Đã xác nhận bằng cách so
  route marker trước/sau khi thêm dòng này.
- **Verify đã chạy thật:** `tsc --noEmit`, `npm run lint`, `npm run build` sạch (kèm 4
  route mới xuất hiện đúng, `/library`+`/leaderboard` là `ƒ`). Đầu-cuối qua Postgres 16
  thật: áp migration Go (0001+0002) rồi 2 migration Next.js (auth + community) lên cùng 1
  DB, seed 1 truyện published + 1 follow + 1 comment qua Drizzle client, chạy `next dev`,
  `curl` cả 4 route — số người theo dõi, nội dung bình luận, điểm chất lượng, và thứ hạng
  leaderboard đều hiện đúng.

## Phase 5 — Monetization

**Trạng thái: cơ chế kỹ thuật (ưu tiên hàng đợi + model tốt hơn) đã code và test thật; đo
nhu cầu (`/upgrade`) đã code. Cổng thanh toán thật thì cố ý chưa làm.**

Plan gốc ghi rõ: "Chỉ làm khi đã có người dùng thường xuyên chạm trần free tier" — điều
kiện đó chưa tồn tại (chưa deploy, chưa người dùng thật). Viết tích hợp thanh toán thật
(chọn Stripe/cổng khác, giá bán cụ thể) trước khi có cả tín hiệu kích hoạt lẫn quyết định
kinh doanh của người dùng sẽ là đoán mò — vượt phạm vi "hoàn thành phase" thành "phát
minh thêm yêu cầu chưa ai đặt ra". Phần còn lại — cơ chế mà một khi có quyết định giá/cổng
thanh toán chỉ cần cắm vào, không cần đổi kiến trúc — đã làm xong:

- **Ưu tiên hàng đợi cho tier trả phí** (`internal/worker/pgstore.go`'s `ClaimNextJob`):
  `ORDER BY (tier = 'paid') DESC, created_at` — job `tier='paid'` luôn được claim trước,
  bất kể xếp hàng sau job `free`/`trial` bao lâu; trong cùng 1 tier vẫn FIFO theo
  `created_at`. Đây chính là "bán tốc độ" theo đúng câu chữ plan gốc. Test thật qua
  Postgres (`TestPGStore_ClaimNextJob_PaidTierJumpsQueue`): seed 2 job free trước, 1 job
  paid sau, xác nhận job paid được claim đầu tiên, rồi 2 job free còn lại theo đúng thứ
  tự FIFO.
- **Model tốt hơn cho vai Writer ở tier trả phí**
  (`internal/worker/runner.go`'s `BuildJobConfig`, tham số `paidWriterOverride`): khi
  `job.Tier == "paid"` và worker được cấu hình `PAID_WRITER_PROVIDER`+`PAID_WRITER_MODEL`
  (`cmd/ainovel-worker/main.go`), ghi đè `Roles["writer"]` cho riêng job đó — copy map
  trước khi ghi để không rò rỉ sang job khác dùng chung `base.Roles`. Cả 2 biến môi
  trường đều để trống mặc định = không ghi đè gì (tier trả phí dùng chung model với tier
  khác) cho tới khi có quyết định model nào là "tốt hơn". Test đơn vị xác nhận cả nhánh
  ghi đè, nhánh bỏ qua khi không phải tier paid, và nhánh không đổi khi override là nil,
  cũng như xác nhận `base.Roles` gốc không bị mutate.
- **`paid` tier trong `src/lib/tiers.ts`**: cùng `budgetUsd`/`maxChapters` với `free` —
  Phase 5 bán tốc độ + model, không bán thêm số chương/ngân sách. Chưa có luồng nào tự
  gán tier này cho job (`createNovel` chỉ chọn `trial`/`free`) — gán `paid` hôm nay là
  thao tác DB thủ công, đúng như kỳ vọng khi chưa có cổng thanh toán.
- **`/upgrade`** (Next.js, cố ý KHÔNG phải trang thanh toán): giải thích gói trả phí bán
  gì, nói rõ "chưa mở bán — chưa có cổng thanh toán", và chỉ có nút "Đăng ký quan tâm" ghi
  vào bảng mới `paid_interest` (user sở hữu, FK tới `users`, không thu thông tin thanh
  toán nào) — mục đích là đo nhu cầu thật trước khi đầu tư vào tích hợp thanh toán, cùng
  tinh thần với Phase 0 "đo trước khi xây". Verify: build/lint/tsc sạch, route hiện `ƒ`
  (dynamic, không bị đóng băng lúc build), curl không đăng nhập thấy đúng thông báo "chưa
  mở bán" + lời mời đăng nhập, không thấy form nào đòi thông tin thanh toán.

Còn lại để "hoàn thành" Phase 5 theo nghĩa đầy đủ (có doanh thu thật): quyết định kinh
doanh về giá + chọn cổng thanh toán (của người dùng, không phải quyết định kỹ thuật), sau
đó tích hợp cổng đó — cả hai đều chờ tín hiệu kích hoạt (người dùng thật chạm trần free
tier) chưa tồn tại.

## Rủi ro cần theo dõi

- **Chất lượng model rẻ với tiếng Việt chưa được kiểm chứng** → Phase 0 tồn tại chính vì
  rủi ro này, và đang bị hoãn. Giảm nhẹ tạm thời: gán model theo vai (Writer rẻ,
  Architect/Editor tốt hơn) nếu Phase 0 sau này cho thấy Writer rẻ không đạt.
- **Ước tính $/chương có thể sai vài lần** — đã từng sai 16-33 lần một lần trong chính
  tài liệu này (xem business-model.md). Không cam kết giá bán nào trước khi có Phase 0.
- **`BudgetConfig.BookUSD` mặc định đang dùng số ước tính** — nếu $/chương thật cao hơn
  ước tính, ngân sách có thể cắt ngang một cuốn chưa xong giữa chừng.
- **Lạm dụng đăng ký hàng loạt** → xác minh email (đã có, Auth.js) + hạn mức/tài khoản
  (đã có, tier trial) + hạn mức ngày toàn hệ thống (**đã code**, xem mục Phase 2 —
  `DAILY_BUDGET_USD`, lớp cuối đảm bảo tệ nhất là hết ngân sách ngày, không phải cháy
  toàn bộ ngân sách). Còn thiếu để đóng hoàn toàn vòng lặp: chưa có cảnh báo/thông báo chủ
  động cho vận hành viên khi chạm ngưỡng (hiện chỉ log `slog.Warn`).
- **Phạm vi nghĩa vụ pháp lý với nội dung văn bản thuần** — điều khoản nhãn AI nêu rõ
  ảnh/video/âm thanh; phạm vi với văn bản cần luật sư xác nhận (xem
  market-research.md). Cách an toàn trong lúc chờ: cứ gắn nhãn.
- **Quan hệ với web UI cục bộ của `ainovel-cli`** (dự án trước, đã xong Phase 0 của
  chính nó — xem `docs/web-ui.md`): nếu nền tảng trở thành sản phẩm chính, web UI cục bộ
  hạ ưu tiên, phục vụ nhóm power user/mã nguồn mở. Cần tránh xây trùng hai giao diện.
- **`nodemailer` CVE mức cao chưa có bản vá** — xem chi tiết ở mục Phase 2.

## Kiểm chứng theo từng phần

- **Phase 0:** so `meta/usage.json` giữa các model; đọc thủ công 10 chương tiếng Việt để
  chấm chất lượng.
- **Worker:** đã test — xem mục Phase 1. Còn thiếu: test Go dựng job chết giữa chừng và
  resume với engine thật (cần key LLM).
- **Hạn mức:** test đặt `BookUSD` cực thấp → xác nhận engine dừng thật (chưa chạy, cần
  key LLM).
- **Nền tảng:** đăng ký từ máy sạch → tạo truyện → đọc → không cài gì, không nhập key
  (chưa chạy, cần SMTP + key LLM thật).
- **Phase 3 (provenance + cổng chất lượng):** đã verify đầu-cuối qua Postgres 16 thật +
  `next dev` — xem chi tiết ở mục Phase 3. Còn thiếu: SEO thật (kiểm tra trang truyện
  render đầy đủ khi tắt JavaScript) chỉ làm được sau khi deploy, vì cần domain thật để
  Google index; cũng cần review thật (key LLM thật) thay vì dữ liệu seed thủ công.
- **Phase 4 (cộng đồng):** đã verify đầu-cuối qua Postgres 16 thật + `next dev` — xem chi
  tiết ở mục Phase 4.

## File/tài sản tái dùng từ engine

- `internal/host/host.go` — `New()`, engine, `Resume()` (khôi phục sau sự cố)
- `internal/host/budget.go` — `BudgetSentinel`, chặn chi phí cứng
- `internal/host/book_lock.go` — cô lập theo thư mục bằng flock (đa job)
- `internal/entry/headless/run.go` — chạy trọn cuốn không cần TUI (mẫu tham khảo cho
  worker, worker không gọi trực tiếp hàm này vì cần harvest có cấu trúc thay vì in ra
  terminal — xem architecture.md)
- `internal/store/usage.go` + `internal/domain/usage.go` — đo chi phí thật
- `internal/domain/review.go` — `ReviewEntry`, `DimensionScore` (cổng chất lượng Phase 3)
- `internal/store/world.go` — `LoadAllReviews()`, `LoadForeshadowLedger()` (nguồn dữ liệu
  harvest Phase 3)
- `internal/diag/diag.go` — `ThresholdDimScoreLow` (mượn làm ngưỡng cổng chất lượng Phase 3)
- `internal/host/exp/` — xuất TXT/EPUB cho người dùng tải về
