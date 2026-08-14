# 外部支付管理 API 集成

本文档用于对接外部支付系统（如 `sub2apipay`）与 TokenRouter 的管理 API。其他操作手册见 [指南目录](../index.md)，稳定接口边界见 [HTTP 接口 Project Doc](../../interfaces/http_api.md)。

## 目标

本文覆盖：

- 支付成功后充值
- 用户查询
- 人工余额修正
- 前端购买页参数透传

## 基础地址

- 生产：`https://<your-domain>`

## 认证

推荐使用以下请求头：

- `x-api-key: admin-<64hex>`
- `Content-Type: application/json`
- 幂等接口额外传入 `Idempotency-Key`

管理员 JWT 也可访问管理路由，但服务间调用应使用管理 API Key。

## 一步完成创建并兑换

`POST /api/v1/admin/redeem-codes/create-and-redeem`

该接口原子完成“创建兑换码并兑换到指定用户”。

请求头：

- `x-api-key`
- `Idempotency-Key`

请求体示例：

```json
{
  "code": "s2p_cm1234567890",
  "type": "balance",
  "value": 100.0,
  "user_id": 123,
  "notes": "sub2apipay order: cm1234567890"
}
```

幂等语义：

- 同一 `code` 且 `used_by` 一致：返回 `200`
- 同一 `code` 但 `used_by` 不一致：返回 `409`
- 缺少 `Idempotency-Key`：返回 `400`（`IDEMPOTENCY_KEY_REQUIRED`）

调用示例：

```bash
curl -X POST "${BASE}/api/v1/admin/redeem-codes/create-and-redeem" \
  -H "x-api-key: ${KEY}" \
  -H "Idempotency-Key: pay-cm1234567890-success" \
  -H "Content-Type: application/json" \
  -d '{
    "code":"s2p_cm1234567890",
    "type":"balance",
    "value":100.00,
    "user_id":123,
    "notes":"sub2apipay order: cm1234567890"
  }'
```

## 查询用户（可选前置校验）

`GET /api/v1/admin/users/:id`

```bash
curl -s "${BASE}/api/v1/admin/users/123" \
  -H "x-api-key: ${KEY}"
```

## 余额调整

`POST /api/v1/admin/users/:id/balance`

该接口用于人工补偿或扣减，支持 `set`、`add` 和 `subtract`。

请求体示例（扣减）：

```json
{
  "balance": 100.0,
  "operation": "subtract",
  "notes": "manual correction"
}
```

```bash
curl -X POST "${BASE}/api/v1/admin/users/123/balance" \
  -H "x-api-key: ${KEY}" \
  -H "Idempotency-Key: balance-subtract-cm1234567890" \
  -H "Content-Type: application/json" \
  -d '{
    "balance":100.00,
    "operation":"subtract",
    "notes":"manual correction"
  }'
```

## 购买页与自定义页面参数透传

TokenRouter 打开 `purchase_subscription_url` 或用户侧自定义页面 iframe URL 时，会统一追加：

- `user_id`
- `token`
- `theme`（`light` 或 `dark`）
- `lang`（例如 `zh` 或 `en`，表示当前界面语言）
- `ui_mode`（固定为 `embedded`）
- `src_host`（TokenRouter 页面来源）
- `src_url`（TokenRouter 当前页面 URL）

示例：

```text
https://pay.example.com/pay?user_id=123&token=<jwt>&theme=light&lang=zh&ui_mode=embedded&src_host=https%3A%2F%2Frouter.example.com&src_url=https%3A%2F%2Frouter.example.com%2Fpurchase
```

`token` 是用户 Bearer 凭据。只允许把购买页或自定义页面配置为受信任的 HTTPS 来源；接收方不得记录、转发或通过第三方分析脚本暴露该参数。

## 失败处理建议

- 分别持久化支付成功和充值成功状态。
- 回调验签成功后立即标记支付成功。
- 支付成功但充值失败的订单应允许后续重试。
- 同一笔充值因超时或网络错误重试时，保持相同 `code`、请求体和 `Idempotency-Key`。
- 只有发起一笔语义不同的新操作时才生成新的 `Idempotency-Key`。

## `doc_url` 配置建议

- 查看链接：`https://github.com/BrandonVee/TokenRouter/blob/main/docs/guides/payments/admin_integration_api.md`
- 下载链接：`https://raw.githubusercontent.com/BrandonVee/TokenRouter/main/docs/guides/payments/admin_integration_api.md`
