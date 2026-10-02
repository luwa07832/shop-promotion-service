# shop-promotion-service

把优惠券定义、促销规则组合、互斥关系、预算余量与核销记录成可查询的服务，支持按订单试算可用的最优优惠组合，并追溯每一次核销、回滚动作与核销凭证。

## 运行要求

- Go 1.26 或以上
- SQLite（本服务自带存储，不需要外部数据库）

## 构建、测试与启动

```bash
go build ./...
go test ./...
go run .
```

服务默认监听 `127.0.0.1:8080`。可用环境变量覆盖：

| 变量 | 默认值 | 用途 |
|---|---|---|
| `ADDR` | `127.0.0.1:8080` | HTTP 监听地址 |
| `DB_PATH` | `shop-promotion-service.db` | SQLite 数据库文件路径 |

## 已公开的入口

### `GET /healthz`

返回服务与存储状态。正常时 HTTP 200：

```json
{"status":"ok","database":"ok"}
```

存储不可用时 HTTP 503：

```json
{"error":{"code":"storage_unavailable","message":"database is not available"}}
```

### `POST /v1/promotions`

创建促销活动定义。请求体是单个 JSON 对象：

```json
{
  "name": "国庆满减",
  "budget_total_cents": 100000,
  "rules": [
    {
      "id": "r1",
      "kind": "order_threshold",
      "min_order_amount_cents": 5000,
      "discount_amount_cents": 500,
      "stackable": true,
      "mutually_exclusive_rule_ids": ["r2"]
    }
  ]
}
```

字段约定：

- `name`：非空字符串。
- `budget_total_cents`：以分计价的预算总额，必须是大于 0 的整数。
- `rules`：非空数组。每条规则要求：
  - `id`：本次请求内唯一的非空字符串；
  - `kind`：固定为 `order_threshold`；
  - `min_order_amount_cents`、`discount_amount_cents`：非负整数，且优惠额不能大于门槛额；
  - `stackable`：布尔值；
  - `mutually_exclusive_rule_ids`：字符串数组，只能引用本次请求中的其他规则 id，表示所列规则与当前规则不能同时适用。

创建成功返回 HTTP 201：

```json
{
  "promotion": {
    "id": "服务生成的活动 id",
    "name": "国庆满减",
    "rules": [],
    "budget_total_cents": 100000,
    "budget_used_cents": 0,
    "status": "draft"
  }
}
```

`budget_used_cents` 固定为 0，`status` 固定为 `draft`。活动持久化在 SQLite 中，服务重启后仍可查询。

### `GET /v1/promotions/:id`

按服务生成的 id 查询活动，返回 HTTP 200 与创建时相同的顶层 `promotion` 对象；读取不改变活动。id 不存在、为空或无法识别时返回 HTTP 404。

## 业务错误码

| HTTP | code | 触发场景 |
|---|---|---|
| 400 | `invalid_promotion_input` | 创建请求的 JSON 非法，或 name/rules/规则 id/kind/互斥引用/金额等字段不满足约定 |
| 404 | `promotion_not_found` | 查询的活动 id 不存在、为空或无法识别 |

当前版本只提供活动定义的创建与查询，不包含订单试算、券核销或回滚。

## 错误约定

所有错误响应都是单个顶层 `error` 对象，包含 `code` 与 `message` 两个字符串字段；`message` 不包含 SQL、堆栈或文件路径。
