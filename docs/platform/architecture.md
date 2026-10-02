# Kiến trúc kỹ thuật

## Hai repo, ranh giới trách nhiệm rõ ràng

```
ainovel-cli (Go, repo hiện tại)          ainovel-platform (repo mới, sibling cục bộ)
├── engine + host.Host                   ├── Next.js (web app, SSR cho SEO)
├── headless.Run() ─── đã có             ├── Postgres (users, novels, jobs, ...)
└── cmd/ainovel-worker  ← Phase 1        └── (sau) object storage cho text chương
         │                                        │
         └────────── Postgres job queue ──────────┘
```

- **`ainovel-cli`** sở hữu engine sinh truyện và `cmd/ainovel-worker` — worker chạy
  job sinh truyện, KHÔNG có UI, không phục vụ HTTP cho người dùng cuối.
- **`ainovel-platform`** sở hữu trải nghiệm người dùng: đăng ký, tạo truyện, đọc
  truyện, cộng đồng. KHÔNG gọi thẳng engine — chỉ ghi vào Postgres.

### Vì sao không để Next.js gọi thẳng engine

Sinh một cuốn truyện mất hàng giờ. Đây phải là background job, không thể là một HTTP
request chờ phản hồi. Postgres-as-queue là lựa chọn đơn giản nhất ở quy mô hiện tại —
đằng nào cũng cần Postgres cho dữ liệu người dùng/truyện, nên không cần thêm
Redis/RabbitMQ chỉ để làm hàng đợi.

### Vì sao Next.js hợp ở đây, khác với web UI cục bộ của CLI

Trang truyện công khai cần SSR để được Google index — đây là kênh phân phối miễn phí và
cộng dồn duy nhất khả thi cho một solo dev không có kênh truyền thông (xem
[market-research.md](./market-research.md)). Web UI cục bộ của `ainovel-cli` (dự án
trước, Vite + React, phục vụ vận hành CLI qua trình duyệt) không cần SSR vì chỉ chạy
trên máy người dùng, không cần SEO.

## Worker (`cmd/ainovel-worker`, trong repo `ainovel-cli`)

Poll bảng `generation_jobs` trong Postgres, claim bằng `FOR UPDATE SKIP LOCKED` (SQL
tiêu chuẩn cho hàng đợi đa worker — N worker process không bao giờ giành cùng một job,
đã kiểm chứng bằng test 8 worker đồng thời × 8 job, không job nào bị claim 2 lần). Mỗi
job chạy trong một thư mục riêng (`job.WorkspaceDir`), dựng `bootstrap.Config` với key
của nền tảng + `BudgetConfig` hạn mức cứng, gọi thẳng `host.New()` + engine — không viết
lại logic sinh truyện.

### Tái sử dụng gần như toàn bộ engine có sẵn

- `host.New()` + `Resume()`/`StartPrepared()` — chạy trọn một cuốn, đúng luồng
  `internal/entry/headless/run.go` đã dùng cho chế độ headless, chỉ khác chỗ tiêu thụ
  event: worker ghi tiến độ/chương vào Postgres thay vì in ra terminal.
- **`bookLease`** (`internal/host/book_lock.go`) — khoá theo thư mục bằng flock, tự động
  bên trong `host.New()`. Hệ quả: N job đồng thời chỉ cần N thư mục riêng biệt, **không
  cần sửa code gì thêm** để chạy nhiều worker song song.
- **checkpoint + `Resume()`** — job chết giữa chừng (worker crash, restart, deploy) chạy
  tiếp được từ checkpoint gần nhất, không mất tiền đã chi cho phần đã sinh.
- **`BudgetSentinel` + `meta/usage.json`** — chặn chi phí cứng và đo chi phí thật, không
  cần viết lại (xem [business-model.md](./business-model.md)).

### Cơ chế mới do worker tự viết (không có sẵn trong engine)

- **Chặn số chương** qua `job.MaxChapters`, mượn đúng kỷ luật "abort rồi drain tới
  `Done()`" từ `internal/eval/runner.go` — engine harness cho eval từng gặp panic
  "send on closed channel" khi return sớm sau abort thay vì đợi kênh `Done()` đóng; worker
  áp dụng lại đúng kỷ luật đó để tránh lặp lại lỗi.
- **Harvest tiến độ**: đọc `meta/progress.json` định kỳ (không parse text sự kiện — không
  có `Category` riêng cho "chapter committed" trong hệ thống event của engine), diff
  danh sách chương đã hoàn thành so với lần đọc trước, đẩy chương mới sang `ChapterStore`.
- **`ChapterStore`** — interface pluggable; `LocalChapterStore` (ghi file cục bộ) là bản
  đứng chỗ cho object storage thật, đổi implementation sau không cần đổi code gọi.
- **Không set `AskUser` handler.** Một worker không người trông không có ai trả lời câu
  hỏi tương tác. `tools.AskUserTool.Execute` (`internal/tools/ask_user.go`) đã tự xử lý
  handler rỗng: trả lời "không hỗ trợ hỏi tương tác, tự quyết định và tiếp tục" cho LLM
  — nên **không cần code thêm gì** cho ca này, engine không bao giờ treo chờ người.

## Schema Postgres

Định nghĩa tại `internal/worker/migrations/0001_init.sql` (repo `ainovel-cli`), tự áp
dụng mỗi khi worker mở kết nối — idempotent qua bảng `schema_migrations` riêng.

| Bảng | Sở hữu bởi | Mục đích |
|---|---|---|
| `users` | **Dùng chung** worker + web app | Định danh người dùng |
| `novels` | worker (ghi), web app (đọc + tạo job) | Metadata truyện, kèm `quality_score`/`published_at` (Phase 3) |
| `chapters` | worker (ghi), web app (đọc) | Chương đã hoàn thành + vị trí lưu trữ |
| `generation_jobs` | web app tạo job `queued`; worker cập nhật trạng thái | Hàng đợi sinh truyện |
| `usage_ledger` | worker (ghi) | Chi phí thật từng job, nguồn dữ liệu tính giá bán |
| `chapter_reviews` (Phase 3) | worker (ghi) | Điểm review 7 chiều của Editor, theo chương/arc/global |
| `foreshadows` (Phase 3) | worker (ghi) | Sổ cái phục bút/伏笔 |
| `accounts`, `sessions`, `verification_token` | **web app** (Auth.js) | Đăng nhập magic-link |

Hạn mức ngày toàn hệ thống (lớp chặn chi phí số 3, xem business-model.md) không có bảng
riêng — `PGStore.TodaySpendUSD()` tính `SUM(cost_usd)` theo ngày trên `usage_ledger` (đã
có index hỗ trợ), worker tự kiểm tra trước mỗi lần claim job (biến môi trường
`DAILY_BUDGET_USD`, xem roadmap.md Phase 2).

### Ranh giới sở hữu schema — quan trọng, đã kiểm chứng bằng Postgres thật

`src/db/schema.ts` (repo `ainovel-platform`) **mirror** đúng các bảng worker sở hữu ở
trên — dùng để truy vấn type-safe, **không được** chạy `drizzle-kit generate`/`push`
trên các bảng này, vì worker tự áp migration của chính nó khi khởi động; hai hệ thống
cùng quản lý một bảng sẽ tạo ra hai lịch sử migration xung đột nhau.

`src/db/auth-schema.ts` (`accounts`/`sessions`/`verification_token`) mới thật sự do
Next.js sở hữu. `drizzle.config.ts` trỏ đúng vào file này, nên `drizzle-kit` về mặt cấu
trúc không thể đụng vào bảng của worker.

`users` là ngoại lệ duy nhất: dùng **chung** giữa hai hệ thống — một bảng định danh, để
tránh danh tính đăng nhập (Auth.js) và danh tính người dùng nền tảng (worker) lệch nhau
cho cùng một người. Vì `@auth/drizzle-adapter` đòi hỏi đúng shape cột (`name`, `image`,
`emailVerified`) trên bảng users nó dùng để đăng nhập, migration Go
(`0001_init.sql`) đã thêm hai cột `name`/`image` (nullable) — worker không đọc/ghi hai
cột này, chỉ tồn tại để phục vụ Auth.js.

#### Cạm bẫy đã gặp thật và cách giải quyết

`drizzle-kit generate` tự động kéo `CREATE TABLE "users"` vào migration sinh ra, vì
`auth-schema.ts` phải import bảng `users` thật để khai foreign key
(`accounts.user_id`/`sessions.user_id` tham chiếu `users.id`). Chạy migration đó nguyên
văn sẽ đụng độ với bảng `users` mà worker đã tạo trước.

Thử `drizzle-kit push` (diff trực tiếp với database thay vì lịch sử migration) thì gặp
prompt tương tác xác nhận xung đột tên bảng — không chạy được ngoài terminal thật (CI,
agent, script tự động).

**Giải pháp đã áp dụng và kiểm chứng:** chạy `drizzle-kit generate`, sau đó **xoá tay
khối `CREATE TABLE "users"`** trong file migration sinh ra (giữ nguyên hai câu
`ALTER TABLE ... ADD CONSTRAINT ... FOREIGN KEY` bên dưới, vì các ràng buộc khoá ngoại
đó vẫn cần thiết), rồi `drizzle-kit migrate` để áp. Đã kiểm chứng bằng cách: dựng
Postgres 16 sạch, cho worker Go tự áp `0001_init.sql` trước, sau đó áp migration đã sửa
của Next.js lên cùng database — kết quả đúng 9 bảng, `users` có đủ cột cả hai bên cần,
`accounts`/`sessions` có khoá ngoại đúng vào `users` với `ON DELETE CASCADE`, không có
lỗi đụng độ. Migration đã sửa được commit sẵn trong `drizzle/0000_chemical_sauron.sql`
của repo `ainovel-platform` — không cần sinh lại trừ khi `auth-schema.ts` thay đổi.

## Luồng tạo truyện, đầu đến cuối

1. Người dùng đăng nhập (Auth.js, magic-link qua email — không mật khẩu, không API key
   riêng), điền form tạo truyện.
2. Server action `createNovel` (`ainovel-platform/src/app/novels/new/actions.ts`) chèn
   một dòng `novels` + một dòng `generation_jobs` với `status='queued'`. **Chính lệnh
   INSERT này là hàng đợi** — không có cuộc gọi HTTP nào tới worker.
3. Worker (poll định kỳ, mặc định 5 giây) claim job bằng `FOR UPDATE SKIP LOCKED`, tạo
   thư mục làm việc riêng, chạy engine.
4. Worker định kỳ ghi heartbeat + harvest chương mới hoàn thành vào Postgres +
   `ChapterStore`.
5. Khi xong (hoặc chạm hạn mức chi phí/số chương), worker ghi usage cuối cùng, cập nhật
   trạng thái `novels`/`generation_jobs`.
6. Trang `/novels/[id]` đọc trạng thái + nội dung chương trực tiếp từ Postgres +
   `ChapterStore` (hiện tại: file cục bộ trên volume dùng chung, không có API polling
   tự động — người dùng phải tải lại trang để thấy tiến độ mới).

## Nhãn AI bắt buộc

Theo yêu cầu pháp lý (xem market-research.md mục 3), mọi trang hiển thị hoặc tạo ra nội
dung truyện phải có thông báo AI. Component dùng chung:
`ainovel-platform/src/components/AiDisclosureBanner.tsx`, đã gắn ở trang tạo truyện và
trang đọc truyện.

## Docker / triển khai cục bộ

`docker-compose.yml` (repo `ainovel-cli`) có thêm profile `platform`
(`docker compose --profile platform up`), gồm `postgres` + `ainovel-worker`, không ảnh
hưởng hai service CLI/web cục bộ đã có từ trước (`ainovel`, `ainovel-web`).
`Dockerfile` build cả hai binary `ainovel-cli` và `ainovel-worker` từ cùng builder stage.
