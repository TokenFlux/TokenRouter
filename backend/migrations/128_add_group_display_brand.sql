-- 为分组增加模型广场展示品牌。
-- display_brand 只影响前台展示，不参与网关路由、账号调度或协议转换。
ALTER TABLE groups ADD COLUMN IF NOT EXISTS display_brand varchar(50) NOT NULL DEFAULT '';

COMMENT ON COLUMN groups.display_brand IS '模型广场展示品牌；为空时前端使用分组名称兜底。';
