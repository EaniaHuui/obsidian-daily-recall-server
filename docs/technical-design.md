# Obsidian 每日回顾技术设计（当前实现）

## 1. 总览

系统采用“本地预提交 + 服务端定时推送”的模式：

1. 插件在用户打开 Obsidian 时，扫描本地笔记并补齐未来 N 天库存（1~30 天）。
2. 服务端按用户时区与推送时间处理当日队列。
3. 推送通道按用户开关分发：RSS、Cubox（可同时开启）。

## 2. 当前架构

```text
Obsidian Plugin (TypeScript)
      |  HTTPS
      v
Go Server (API + Worker)
      |
   SQLite (WAL)
      |
  +---+-------------------------+
  |                             |
RSS Feed                    Cubox API
```

说明：

- 已不再依赖 Bark 主流程。
- `sync_mode` 固定为 `local`，以本地抽取队列为主。

## 3. 数据模型（关键表）

基础表：

- `users`, `user_tokens`, `user_settings`, `notes`, `ai_usage`

回顾与推送：

- `user_prompt_settings`：`daily_push_count`, `summary_prompt`
- `scheduled_recalls`：本地预提交库存（按天 + 槽位）
- `push_history`：RSS 明细来源

通道相关：

- `user_channel_settings`：`enable_rss`, `enable_cubox`, `cubox_api_url_enc`, `cubox_folder`, `cubox_tags`
- `rss_feeds`：每用户私有 RSS token

迁移文件：

- [server/db/migrations/001_init.sql](../server/db/migrations/001_init.sql)
- [server/db/migrations/002_recall_queue.sql](../server/db/migrations/002_recall_queue.sql)
- [server/db/migrations/003_channel_settings.sql](../server/db/migrations/003_channel_settings.sql)

## 4. 内容生成规则

- 摘要功能已经移除，不再作为当前产品功能的一部分。
- 服务端推送时直接使用正文内容。
- `user_prompt_settings.daily_push_count` 继续保留，用于每日条数配置。
- `user_prompt_settings.summary_prompt` 作为兼容旧数据字段保留，但不再参与业务逻辑。

## 5. 详情页渲染规则

RSS 详情页由服务端直接输出纯 HTML：

- 直接渲染完整正文（Markdown 渲染后 HTML）
- 去掉摘要区块
- 去掉折叠组件（无 `details/summary`）

实现位置：

- [server/handlers/rss.go](../server/handlers/rss.go)

## 6. 核心 API（当前）

认证：

- `POST /api/v1/auth/register`
- `POST /api/v1/auth/login`
- `POST /api/v1/auth/logout`
- `GET /api/v1/auth/sessions`
- `DELETE /api/v1/auth/sessions/{session_id}`

设置与通道：

- `GET /api/v1/user/settings`
- `PUT /api/v1/user/settings`
- `GET /api/v1/user/rss`
- `POST /api/v1/user/rss/reset`

库存队列：

- `POST /api/v1/recalls/queue`
- `GET /api/v1/recalls/queue/status`

历史与公开订阅：

- `GET /api/v1/push/history`
- `GET /api/v1/push/history/{id}`
- `GET /api/v1/rss/{token}`
- `GET /api/v1/rss/{token}/item/{id}`

## 7. 调度与执行

Worker 每 30 秒 tick：

1. 按用户时区判断是否到达（或超过）`push_time`
2. 处理 `scheduled_recalls` 中 `scheduled_date <= today` 且 `status=queued`
3. 对每条记录执行：
   - 读取用户设置（通道开关、每日条数）
   - 生成列表预览文本
   - 分发到开启通道（RSS/Cubox）
   - 更新 `done/failed`

## 8. 部署（已验证流程）

约束：必须本地构建，不在远端编译。

### 8.1 本地构建 Linux 二进制

```bash
cd /path/to/obsidian-recall/server
export ZIG=/path/to/zig
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
CC="$ZIG cc -target x86_64-linux-gnu" \
CXX="$ZIG c++ -target x86_64-linux-gnu" \
go build -o ./deploy/obsidian-recall-server-linux-amd64 .
```

### 8.2 上传并重启

```bash
scp ./deploy/obsidian-recall-server-linux-amd64 user@your-server:/tmp/
ssh user@your-server
sudo systemctl stop obsidian-recall
sudo cp /tmp/obsidian-recall-server-linux-amd64 /opt/obsidian-recall/bin/obsidian-recall-server
sudo chmod +x /opt/obsidian-recall/bin/obsidian-recall-server
sudo systemctl start obsidian-recall
```

### 8.3 验收

- 服务健康：`curl http://127.0.0.1:8080/healthz`
- 外部入口：`https://your-domain.example/healthz`
- nginx：`<public-port> -> 127.0.0.1:8080`

## 9. 安全要点

- token 仅存储 SHA-256（明文只返回一次）
- 机密字段 AES-256-GCM 存储（Cubox API URL / AI Key）
- 所有用户数据查询必须带 `user_id` 约束
