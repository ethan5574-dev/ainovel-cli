# Nghiên cứu thị trường

Bối cảnh: người dùng muốn mở một nền tảng web dạng forum/social nơi mọi người vừa dùng
được engine sinh tiểu thuyết của `ainovel-cli`, vừa chia sẻ tác phẩm tạo ra. Trước khi
thiết kế, cần trả lời: thị trường "truyện AI" đang ở trạng thái nào, và sản phẩm này nên
định vị ra sao để không lặp lại sai lầm đã xảy ra ở nơi khác.

## 4 phát hiện định hình toàn bộ thiết kế

### 1. Thị trường "chỗ đăng truyện AI" không thiếu — đang thừa và bị dọn dẹp gắt

Fanqie (nền tảng đọc truyện của ByteDance) từ chối hơn 104.000 bản thảo kém chất lượng
chỉ trong tháng 6/2026, phạt 49 tác giả top, và cấm vĩnh viễn 17 tài khoản vì dùng AI
viết toàn bộ tác phẩm. Jinjiang (Tencent) giới hạn việc dùng AI chỉ cho tra cứu và soát
lỗi, không cho sinh nội dung. Nhiều nền tảng khác áp trần số chữ được đăng mỗi ngày để
ngăn ngập lụt nội dung AI.

Kết luận của Rest of World (báo cáo được tham khảo): các nền tảng đang **rút lui** khỏi
việc cho phép truyện AI tự do — bằng chứng cho thấy truyện AI thuần (không kiểm soát
chất lượng) không giữ chân được người đọc, và tạo ra nhiều chi phí vận hành (dọn rác,
xử lý khiếu nại) hơn giá trị nó mang lại.

**Hệ quả thiết kế:** không được xây "nơi đăng truyện AI không giới hạn". Đó là mô hình
đã được thị trường chứng minh là hỏng, không phải một giả thuyết chưa kiểm chứng.

### 2. Chuẩn mực đang hình thành là công khai nguồn gốc, không phải cấm hoàn toàn

Royal Road (một trong các nền tảng đọc truyện web lớn) cho phép truyện có thành phần AI
miễn là gắn tag bắt buộc công khai — nhưng cấm AI trong phần bình luận/diễn đàn, để giữ
"tiếng người" ở không gian tương tác. Amazon KDP (xuất bản sách) yêu cầu tác giả khai báo
nội dung do AI hỗ trợ/tạo ra, không cấm, và việc khai báo này đã trở thành bình thường
trong ngành — không có bằng chứng nó ảnh hưởng tiêu cực đến doanh số.

Khảo sát độc giả (được tham khảo trong nghiên cứu): 60-70% độc giả vẫn đọc tiếp nếu nội
dung hay, bất kể có phải AI hay không; chỉ 15-20% tẩy chay hoàn toàn bất kỳ nội dung nào
có AI tham gia.

**Hệ quả thiết kế:** minh bạch nguồn gốc là chiến lược khả thi, không phải hy sinh thị
trường. Không cần giấu việc dùng AI; cần làm cho việc dùng AI đáng tin.

### 3. Việt Nam đã có luật đang có hiệu lực, không phải rủi ro tương lai

- **Luật 134/2025/QH15** về AI — hiệu lực từ 1/3/2026.
- **Nghị định 142/2026/NĐ-CP** — hiệu lực từ 1/5/2026.

Hai văn bản này bắt buộc: (a) gắn nhãn nội dung do AI tạo ra, (b) thông báo cho người
dùng khi họ đang tương tác với một hệ thống AI. Có ân hạn đến 1/3/2027 cho nhà cung cấp
dịch vụ đã hoạt động trước ngày luật có hiệu lực.

**Phạm vi chưa rõ:** điều khoản về nhãn nêu rõ áp dụng cho ảnh/video/âm thanh; phạm vi
áp dụng với nội dung văn bản thuần (như tiểu thuyết) chưa được nêu tường minh trong các
nguồn đã tra cứu — cần luật sư xác nhận trước khi ra mắt công khai. Cách tiếp cận an
toàn trong lúc chờ xác nhận: cứ gắn nhãn như thể luật áp dụng, vì chi phí gắn nhãn gần
như bằng không còn chi phí không gắn nhãn (nếu luật áp dụng) là rủi ro pháp lý thật.

**Hệ quả thiết kế:** nhãn AI bắt buộc là yêu cầu **pháp lý** của v1, không phải tính
năng có thể hoãn sang phase sau. Đã triển khai ngay từ Phase 2 (xem
[architecture.md](./architecture.md)).

### 4. Độc giả phản đối những thứ rất cụ thể — và đều đo được

Từ các phản hồi độc giả được tham khảo, bốn nguyên nhân gây khó chịu lặp lại nhiều nhất:

- Câu văn sáo rỗng theo khuôn "không phải X, mà là Y" — dấu hiệu văn phong AI dễ nhận ra.
- Prompt hoặc chỉ dẫn hệ thống bị lộ ra trong chính văn bản truyện (lỗi kỹ thuật của
  quy trình sinh, không phải lỗi nội dung).
- Ẩn dụ/so sánh kỳ quặc, không tự nhiên.
- Tốc độ đăng bài quá nhanh (nhiều chương/ngày) làm hỏng trải nghiệm đọc, tạo cảm giác
  "sản xuất hàng loạt" thay vì sáng tác.

**Hệ quả thiết kế:** đây đều là tiêu chí đo được, không phải cảm tính. Cổng chất lượng ở
Phase 3 dùng điểm review 7 chiều của Editor (đã có sẵn trong engine) để lọc chính những
vấn đề này trước khi xuất bản công khai.

## Kết luận chiến lược: định vị sản phẩm

Điểm khác biệt duy nhất, không sao chép được, của dự án này **không phải là khả năng
sinh truyện** — bất kỳ ai tích hợp một LLM cũng làm được việc đó. Điểm khác biệt là
**hồ sơ sản xuất** mà engine đã ghi lại trong suốt quá trình sinh:

- Outline phân tầng (volume → arc → chapter)
- Điểm review 7 chiều của Editor cho từng chương
- Sổ cái phục bút/伏笔 (foreshadow ledger) — theo dõi các chi tiết cài cắm và khi nào
  được giải quyết
- Tóm tắt arc/volume
- Checkpoint từng bước của quá trình sinh

Không nền tảng đọc truyện AI nào khác hiện cho người đọc xem được **"cuốn này được làm
ra như thế nào"**. Đây là dữ liệu engine đã có sẵn (không cần xây mới), chỉ cần một giao
diện hiển thị nó ra — đó chính là trang provenance ở Phase 3.

**Định vị:** minh bạch quy trình + kiểm soát chất lượng, không phải "sinh truyện không
giới hạn". Phù hợp trực tiếp với phát hiện #1 (mô hình không giới hạn đã hỏng) và #2
(minh bạch là chuẩn mực đang hình thành).

## Nguồn tham khảo

Các số liệu và sự kiện trên được tổng hợp từ nghiên cứu thị trường thực hiện trong quá
trình lên kế hoạch dự án (8/2026), bao gồm báo cáo của Rest of World về chính sách nội
dung AI của Fanqie/Jinjiang, chính sách công khai của Royal Road và Amazon KDP, và văn
bản Luật 134/2025/QH15 + Nghị định 142/2026/NĐ-CP. Số liệu cụ thể (ví dụ: "104.000+ bản
thảo", "49 tác giả", "17 tài khoản") nên được xác minh lại trước khi trích dẫn công khai
(ví dụ trong tài liệu marketing hoặc trang provenance), vì đây là tổng hợp từ nghiên cứu
tại một thời điểm, không phải số liệu do dự án này tự thu thập.
