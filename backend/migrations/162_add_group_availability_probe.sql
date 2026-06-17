-- 分组主动可用性探测配置与结果表。
-- 配置按分组保存；结果表只记录探测聚合所需信息，模型广场仅暴露聚合后的可用率。

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS availability_probe_config JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN groups.availability_probe_config IS '分组主动可用性探测配置';

CREATE INDEX IF NOT EXISTS idx_groups_availability_probe_enabled
    ON groups (id)
    WHERE deleted_at IS NULL
      AND status = 'active'
      AND availability_probe_config @> '{"enabled": true}'::jsonb;

CREATE TABLE IF NOT EXISTS group_availability_probe_states (
    group_id BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    next_run_at TIMESTAMPTZ,
    locked_until TIMESTAMPTZ,
    locked_by VARCHAR(128),
    last_status VARCHAR(16),
    last_success BOOLEAN,
    last_latency_ms BIGINT,
    last_error TEXT,
    last_checked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE group_availability_probe_states IS '分组主动可用性探测调度状态';

CREATE INDEX IF NOT EXISTS idx_group_availability_probe_states_due
    ON group_availability_probe_states (next_run_at)
    WHERE next_run_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS group_availability_probe_results (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    account_id BIGINT,
    model_id VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL,
    success BOOLEAN NOT NULL DEFAULT false,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_message TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE group_availability_probe_results IS '分组主动可用性探测结果';

CREATE INDEX IF NOT EXISTS idx_group_availability_probe_results_group_time
    ON group_availability_probe_results (group_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_group_availability_probe_results_cleanup
    ON group_availability_probe_results (created_at);
