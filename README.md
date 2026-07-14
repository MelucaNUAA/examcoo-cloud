# ExamCoo Cloud

ExamCoo 自动答题工具的云部署版本，支持 10 并发任务、移动端适配和二维码扫描。

## 功能特性

- 保留桌面版全部功能（自动答题、扫题入库、题库管理）
- 10 并发任务支持
- WebSocket 实时日志推送
- 二维码扫描获取考试链接（摄像头 + 图片）
- 移动端响应式适配
- Docker 一键部署

## 快速开始

### 本地运行

```bash
# 编译
go build -o examcoo-cloud ./cmd/server

# 运行
./examcoo-cloud
# 访问 http://localhost:8080
```

### Docker 部署

```bash
# 构建镜像
docker build -t examcoo-cloud .

# 运行容器
docker run -d -p 8080:8080 -v examcoo-data:/app/data examcoo-cloud

# 或使用 docker-compose
docker compose up -d
```

### Render 部署

1. 将代码推送到 GitHub 仓库
2. 登录 [render.com](https://render.com)
3. New → Web Service → 选择仓库
4. Render 会自动检测 `render.yaml` 并配置
5. 点击 Create Web Service

免费计划限制：
- 15 分钟无请求后休眠，冷启动约 30 秒
- WebSocket 在休眠后会断开，页面需刷新重连
- 磁盘 1GB 持久化存储

## 目录结构

```
ExamCooCloud/
  cmd/server/main.go       -- 云版入口
  internal/
    api/
      handler.go           -- API 处理函数
      router.go            -- 路由注册
      ws_hub.go            -- WebSocket 管理
      middleware.go         -- 中间件
    core/
      core.go              -- 核心业务逻辑
  frontend/
    index.html             -- 页面结构
    app.js                 -- UI 逻辑
    style.css              -- 样式
  data/                    -- 数据目录（config.json, answer_bank.json）
  Dockerfile
  docker-compose.yml
  nginx.conf.example       -- Nginx 配置示例
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| PORT | 8080 | 监听端口 |
| DATA_DIR | . | 数据文件目录 |

## Nginx 反向代理

参考 `nginx.conf.example` 配置 Nginx，需要特别注意 WebSocket 升级配置。

## 腾讯 EdgeOne 部署

1. 将 Docker 容器部署在云服务器
2. EdgeOne 配置回源规则：
   - `/api/*` 和 `/ws` 回源到服务器
   - `/` 静态资源走 EdgeOne 缓存
3. 开启 WebSocket 支持
4. 配置 SSL 证书
