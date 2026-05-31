# Obsidian 每日回顾服务端

这是 Obsidian 每日回顾的 Go 服务端，负责：

- 匿名初始化与 Token 鉴权
- 用户设置存储
- 本地预提交回顾队列管理
- 定时调度与发送
- RSS 私有订阅输出
- Cubox API 推送
- 可选 AI 摘要

## 技术栈

- Go 1.22+
- SQLite（WAL）
- Nginx 反向代理

## 本地开发

```bash
go test ./...
go run .
```

健康检查：

```bash
curl http://127.0.0.1:8080/healthz
```

## 环境变量

可参考：

- `.env.example`
- `deploy/env.production.example`

关键变量：

- `BASE_URL`：服务端公开访问地址，用于生成 RSS 与详情页链接
- `DB_PATH`：SQLite 文件路径
- `MASTER_KEY`：用于加密存储用户密钥
- `JWT_SECRET`：访问令牌签名密钥

## 部署

推荐流程：

1. 在本地构建 Linux 二进制
2. 上传到服务器
3. 用 systemd 托管
4. 用 Nginx 做 HTTPS 和反向代理

详细设计与部署说明见：

- `docs/technical-design.md`

## 安全说明

- 不要把真实 `BASE_URL`、密钥或 Token 提交进仓库
- 生产环境请强制 HTTPS
- 所有示例配置都应视为占位符
