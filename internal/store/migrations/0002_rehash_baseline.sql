-- 内容哈希参与字段从 (title, content_html, summary) 改为
-- (title, summary, 归一化后的正文文本)，算法变更后旧哈希与任何新值都不相等。
-- 这里统一清空，配合 UpsertEntry 的「空哈希只补基线、不算变化」逻辑，
-- 避免升级后第一次运行把全部历史条目误判成内容更新并触发一轮通知。
UPDATE entries SET content_hash = '';
