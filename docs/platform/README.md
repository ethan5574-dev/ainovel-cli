# Nền tảng cộng đồng tiểu thuyết AI — tài liệu kỹ thuật

Tài liệu cho dự án nền tảng web (forum/social) xây trên engine sinh tiểu thuyết của
`ainovel-cli`. Đây là ghi chép chính thức, tách khỏi lịch sử hội thoại — nếu có mâu
thuẫn giữa tài liệu này và một quyết định cũ hơn trong hội thoại, tài liệu này thắng.

## Đọc theo thứ tự nào

1. **[market-research.md](./market-research.md)** — 4 phát hiện thị trường định hình
   toàn bộ thiết kế, và kết luận định vị sản phẩm. Đọc trước tiên: mọi quyết định kỹ
   thuật sau đây đều bắt nguồn từ đây.
2. **[business-model.md](./business-model.md)** — bài toán kinh tế (giá model, tier,
   3 lớp chặn chi phí), và vì sao ràng buộc "không cài đặt / không thu phí / không
   giữ key hộ" ban đầu tưởng mâu thuẫn nhưng thực ra giải được.
3. **[architecture.md](./architecture.md)** — kiến trúc 2 repo, ranh giới sở hữu
   schema, thiết kế worker. Chi tiết kỹ thuật cho ai code tiếp.
4. **[roadmap.md](./roadmap.md)** — phân kỳ Phase 0–5, trạng thái hiện tại của từng
   phase, rủi ro, kế hoạch kiểm chứng.

## Tóm tắt một câu

Đăng ký bằng email (không cài đặt, không key riêng), sinh truyện chạy trên model rẻ
với hạn mức cứng, xuất bản kèm nhãn AI bắt buộc và trang "hồ sơ sản xuất" minh bạch
quy trình — đặt cược vào **minh bạch + kiểm soát chất lượng**, không phải "sinh
truyện không giới hạn" (mô hình đã được thị trường chứng minh là hỏng).

## Hai repo

- **`ainovel-cli`** (repo này) — engine sinh truyện + `cmd/ainovel-worker` (job runner
  của nền tảng). Xem `docs/architecture.md` (tài liệu engine, khác file này) cho kiến
  trúc lõi; `internal/worker/` cho code nền tảng trong repo này.
- **`../ainovel-platform`** — web app Next.js, sibling repo cục bộ. Xem README của
  repo đó cho hướng dẫn setup.

## Trạng thái tại thời điểm viết tài liệu này (2026-08-08)

| Phase | Trạng thái |
|---|---|
| 0 — Đo giá/chất lượng thật | **Hoãn** theo yêu cầu người dùng; xây tiếp trên số ước tính |
| 1 — Worker + schema | Code xong, test qua Postgres thật, chưa chạy với key LLM thật |
| 2 — Web app tối thiểu | Scaffold xong, build/lint/typecheck sạch, chưa deploy/chưa chạy với SMTP+key thật |
| 3 — Xuất bản + provenance | Xem roadmap.md |
| 4 — Cộng đồng | Xem roadmap.md |
| 5 — Monetization | Xem roadmap.md |

Chi tiết đầy đủ, kể cả các cạm bẫy kỹ thuật đã gặp và cách giải quyết, nằm trong
[roadmap.md](./roadmap.md).
