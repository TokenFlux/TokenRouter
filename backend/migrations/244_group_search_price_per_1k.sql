-- Grok 和通用搜索工具按每 1000 次调用以 USD 显式定价。
-- NULL = 使用代码默认 $10/1k；显式 0 = 免费；>0 = 分组覆盖价。
ALTER TABLE groups ADD COLUMN IF NOT EXISTS search_price_per_1k DECIMAL(20,8);
