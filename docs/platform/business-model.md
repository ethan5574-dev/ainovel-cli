# Business model

## Ràng buộc ban đầu — tưởng mâu thuẫn, thực ra giải được

Người dùng (solo dev) nêu bốn ràng buộc thực tế, mỗi cái đều hợp lý riêng lẻ nhưng
tưởng như mâu thuẫn nhau:

- **Không có kênh truyền thông** → không thể bán sản phẩm thu tiền ngay từ đầu (không
  ai biết để mua).
- **Không thể gánh chi phí LLM không giới hạn** → không thể cho dùng miễn phí vô tội vạ.
- **Không thể bắt cài đặt local** → loại hết người dùng không rành kỹ thuật, tức phần
  lớn độc giả tiềm năng.
- **Không thể giữ API key hộ người dùng** → solo dev chưa đủ uy tín để người dùng tin
  tưởng giao key.

Hai phương án đầu tiên được đề xuất (bắt người dùng tự cài đặt / tự quản lý key) đều bị
từ chối đúng vì lý do trên. Phương án được chọn giải quyết đồng thời cả bốn, dựa trên
một phát hiện định lượng: **nỗi lo chi phí ban đầu được ước tính trên giá của model cao
cấp — model rẻ thực tế rẻ hơn nhiều bậc.**

## Giá thật của model rẻ (nguồn: bảng giá engine đang dùng)

Giá lấy từ `internal/models/models_generated.go` — bảng giá engine tự sinh từ
OpenRouter (lần sinh gần nhất: 2026-05-31), và cũng chính là bảng dùng để tính
`cost_usd` ghi vào `meta/usage.json` sau mỗi lần chạy:

| Model | Input / Output per 1M token | Ước tính $/chương* | 100 chương |
|---|---|---|---|
| Gemini 2.5 Flash-Lite | $0.10 / $0.40 | ~$0.04 | ~$4 |
| DeepSeek V3 (`deepseek-chat`) | $0.2288 / $0.9144 | ~$0.09 | ~$9 |
| Gemini 2.5 Flash | $0.30 / $2.50 | ~$0.14 | ~$14 |

\* Ước tính từ ~10 lượt gọi LLM/chương × ~30k token input + ~2k token output —
**chưa được đo thật**, xem Phase 0 trong [roadmap.md](./roadmap.md).

### Đính chính quan trọng

Một phiên bản ước tính trước đó của tài liệu này ghi giá DeepSeek V3 ở mức
$0.014/$0.028 per 1M — **sai khoảng 16–33 lần** so với giá thật trong bảng của engine.
Hai hệ quả:

1. Mọi con số chi phí trong tài liệu này đã được tính lại theo giá đúng.
2. **Thứ hạng model đảo chiều**: Gemini 2.5 Flash-Lite mới là lựa chọn rẻ nhất, không
   phải DeepSeek như ước tính ban đầu. Do đó Phase 0 đo Flash-Lite làm ứng viên chính
   cho tier miễn phí, DeepSeek chỉ để đối chứng.

### Cảnh báo phương pháp — quan trọng, cần nhớ khi đọc `meta/usage.json`

`cost_usd` được engine tính từ bảng giá **local** nói trên, không phải từ hoá đơn thật
của nhà cung cấp. Bảng này lấy giá từ OpenRouter tại một thời điểm (~2 tháng trước lúc
viết tài liệu này), có thể lệch so với giá gọi thẳng API của nhà cung cấp. Vì vậy:

- Phase 0 **phải đối chiếu `meta/usage.json` với dashboard billing thật** của nhà cung
  cấp ít nhất một lần.
- Nếu hai số lệch nhau, số đáng tin là **token count** (`input`/`output` trong
  `meta/usage.json`), không phải `cost_usd` — vì token count là sự thật đo trực tiếp,
  còn `cost_usd` phụ thuộc vào độ chính xác của bảng giá local.

## Thiết kế: nền tảng chạy inference, hạn mức cứng thay vì key riêng

Người dùng chỉ cần **đăng ký tài khoản web bằng email** — không cài đặt, không nhập
API key, không trả tiền ngay. Nền tảng tự chạy inference bằng key của chính nó, trên
model rẻ, và chặn chi phí bằng ba lớp:

1. **`BudgetSentinel`** (`internal/host/budget.go`, đã có sẵn trong engine) — chặn cứng
   USD/cuốn, hỗ trợ `hardStop` (dừng ngay khi chạm ngưỡng) và cảnh báo theo `warnRatio`,
   tự phát hiện "điểm mù tính tiền" khi model không có giá trong bảng. Dùng nguyên,
   không cần viết mới — đã wiring vào worker ở Phase 1.
2. **`meta/usage.json`** (`internal/store/usage.go`, đã có sẵn) — ghi input/output/
   cache_read/cost_usd thực tế của từng cuốn. Đây vừa là nguồn số liệu để đo (Phase 0),
   vừa là nguồn để tính giá bán sau này (Phase 5).
3. **Hạn mức ngày toàn hệ thống + hàng đợi** (mới, thuộc về nền tảng, chưa code) — khi
   ngân sách của cả hệ thống trong ngày dùng hết, job mới vào hàng đợi thay vì tiếp tục
   tiêu tiền. Đây là lớp chặn cuối cùng cho kịch bản lạm dụng đăng ký hàng loạt.

Hai lớp đầu đã có sẵn trong engine từ trước — không phải xây cho dự án nền tảng, chỉ cần
tái sử dụng đúng cách. Lớp thứ ba là phần thực sự mới, thuộc phạm vi Phase 2/3.

## Thiết kế tier

- **Dùng thử (1 lần/tài khoản):** premise + outline + 5 chương. ~$0.25 trên Flash-Lite
  (~$0.50 nếu dùng DeepSeek). Ở quy mô 1.000 lượt dùng thử, chi phí ~$250 — không còn là
  chi phí bỏ qua được, nên xác minh email là **bắt buộc**, không phải tuỳ chọn, và hạn
  mức ngày toàn hệ thống là lớp chặn có thật, không phải phòng hờ.
- **Miễn phí thường xuyên:** N chương/ngày, xếp hàng khi hết ngân sách ngày của toàn hệ
  thống. Đây trùng khớp một cách tự nhiên với nhịp đăng của truyện dài kỳ (không ai kỳ
  vọng 50 chương/ngày) — ràng buộc chi phí và hình thái sản phẩm khớp nhau, đồng thời
  chặn luôn nguy cơ một tài khoản spam sinh hàng loạt nội dung rác.
- **Trả phí (sau, khi đã có người dùng):** model tốt hơn cho vai trò Writer + không
  phải xếp hàng. Bán đúng lúc người dùng đã viết được ~40 chương — thời điểm họ có ý
  định gắn bó cao nhất, không cần marketing để thuyết phục. Engine đã hỗ trợ gán model
  khác nhau theo từng vai trò (`roles` trong `bootstrap.Config`), nên tier này về mặt kỹ
  thuật chỉ là đổi config, không cần thay đổi engine.

## Giải quyết cold start bằng chính bài toán chi phí

Một cộng đồng ra mắt với thư viện trống sẽ không giữ chân được ai. Giải pháp: tự seed
sẵn một thư viện truyện trước khi mở đăng ký công khai, dùng chính hạ tầng vừa xây.

Với giá đã đính chính (Flash-Lite: một cuốn 30 chương ~$1.2, một cuốn 100 chương ~$4):

| Phương án seed | Chi phí ước tính |
|---|---|
| 25 cuốn × 30 chương | ~$30 |
| 75 cuốn × 30 chương | ~$90 |
| 75 cuốn × 100 chương | ~$300 |

Ước tính ~$30–40 trong bản nháp trước đây (dựa trên giá DeepSeek sai) chỉ đủ mua khoảng
25 cuốn ngắn, không phải 75–100 cuốn hoàn chỉnh như dự tính ban đầu. Quy mô seed thật sự
sẽ chốt sau khi Phase 0 cho số $/chương chính xác — và theo đúng phát hiện thị trường
#1 ([market-research.md](./market-research.md)), ưu tiên **"ít cuốn nhưng đọc được"**
hơn **"nhiều cuốn nhưng dở"**: một thư viện nhỏ nhưng chất lượng tốt phù hợp với định vị
"kiểm soát chất lượng" hơn một thư viện lớn nhưng đầy nội dung yếu.

## Vì sao thiết kế này giải được cả 4 ràng buộc ban đầu

| Ràng buộc | Cách giải quyết |
|---|---|
| Không có kênh truyền thông | Không cần bán ngay — dùng thử + free tier tự thu hút người dùng qua chính chất lượng sản phẩm và kênh SEO (trang truyện công khai, xem Phase 3) |
| Không gánh nổi chi phí không giới hạn | Model rẻ + 3 lớp chặn chi phí (trên) làm chi phí biên rất nhỏ và có trần cứng |
| Không thể bắt cài đặt | Web app, đăng ký bằng email, không cài gì |
| Không giữ key hộ người dùng | Nền tảng tự chạy inference bằng key riêng của mình, người dùng không bao giờ chạm vào key |
