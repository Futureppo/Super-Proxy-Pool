# Super-Proxy-Pool

基于 Go + SQLite + Mihomo 的代理池管理面板。项目提供：

- 密码登录的管理面板
- 订阅管理
- 手动节点管理
- 多代理池管理
- Mihomo 生产实例和探测实例配置下发
- 节点延迟测试、可选测速、SSE 状态推送

当前仓库已经可以直接启动，适合继续迭代和部署。

## 默认配置

- 面板地址：`0.0.0.0:7890`
- 默认密码：`admin`
- SQLite：
  - Docker：`/data/app.db`
  - Windows 本地开发：`./data/app.db`
- 默认订阅同步间隔：`3600` 秒
- 默认延迟测试 URL：`https://cp.cloudflare.com/generate_204`
- 测速：默认关闭

## 本地开发

### 依赖

- Go `1.25+`
- Mihomo 可执行文件

说明：

- 面板本身支持直接 `go run ./cmd/app`
- 即使本地没有 Mihomo，面板也可以启动，CRUD、配置生成、页面开发都可用
- 如果要真实发布代理池、执行延迟测试、执行测速，需要能找到 Mihomo 二进制

### Mihomo 自动发现规则

启动时按下面顺序查找 Mihomo：

1. 环境变量 `MIHOMO_BINARY`
2. 仓库内常见路径
   - `./bin/mihomo` 或 `./bin/mihomo.exe`
   - `./tools/mihomo` 或 `./tools/mihomo.exe`
   - `./deployments/bin/mihomo` 或 `./deployments/bin/mihomo.exe`
   - `./mihomo` 或 `./mihomo.exe`
3. 系统 `PATH`
4. 默认路径
   - Linux/macOS：`/usr/local/bin/mihomo`
   - Windows：`mihomo.exe`

建议开发时直接把 Mihomo 放在仓库根目录下的 `bin/`。

### Windows

直接运行：

```powershell
go run ./cmd/app
```

或者使用启动脚本：

```powershell
powershell -ExecutionPolicy Bypass -File .\deployments\dev.ps1
```

自定义端口或二进制路径：

```powershell
powershell -ExecutionPolicy Bypass -File .\deployments\dev.ps1 `
  -PanelPort 7891 `
  -MihomoBinary C:\tools\mihomo.exe
```

启动后访问：

- [http://127.0.0.1:7890/login](http://127.0.0.1:7890/login)

### Linux / macOS

直接运行：

```bash
go run ./cmd/app
```

或者使用启动脚本：

```bash
bash ./deployments/dev.sh
```

自定义参数：

```bash
bash ./deployments/dev.sh --port 7891 --mihomo-binary /usr/local/bin/mihomo
```

### 运行测试

```bash
go test ./...
```

## Docker 部署

### 构建

```bash
docker build -t super-proxy-pool .
```

### 方式 A：Host 网络，推荐

适用于 Linux 服务器。优点是新增代理池端口时不需要重新发布容器端口。

```bash
docker run -d \
  --name super-proxy-pool \
  --network host \
  --restart unless-stopped \
  -v $PWD/data:/data \
  super-proxy-pool
```

### 方式 B：Bridge 网络 + 预开放端口范围

如果不能使用 host 网络，就提前映射一段端口范围，例如 `18080-18120`。之后在“系统设置”里把“代理池端口范围最小值 / 最大值”设置到同一范围内，系统会拒绝超出范围的代理池端口。

```bash
docker run -d \
  --name super-proxy-pool \
  --restart unless-stopped \
  -p 7890:7890 \
  -p 18080-18120:18080-18120 \
  -v $PWD/data:/data \
  super-proxy-pool
```

### docker-compose

仓库自带示例：

```bash
docker compose up -d super-proxy-pool
```

Host 网络配置：

```bash
docker compose --profile host up -d super-proxy-pool-host
```

## 常用操作

### 登录

1. 打开 `/login`
2. 输入默认密码 `admin`
3. 首次进入后建议立刻在“系统设置”里修改密码

### 添加订阅

1. 进入“订阅管理”
2. 新建订阅，填写名称和订阅 URL
3. 保存后点击“立即同步”
4. 在订阅详情里查看解析到的节点卡片

支持的订阅内容：

- Mihomo / Clash YAML
- Base64 编码后的 URI 列表
- 纯文本 URI 列表

### 添加手动节点

1. 进入“节点管理”
2. 粘贴以下任一种内容
   - 单条 URI
   - 多条 URI
   - Mihomo `proxies` YAML 片段
3. 保存后可在节点卡片上直接测试延迟、测速、启用或禁用

支持的手动节点协议：

- `ss://`
- `trojan://`
- `vmess://`
- `vless://`
- `hysteria2://`
- `tuic://`
- Mihomo YAML proxy 片段

### 创建代理池

1. 进入“代理池设置”
2. 新建代理池
3. 选择协议 `http` 或 `socks`
4. 设置监听地址和端口
5. 从手动节点和订阅节点中选择成员
6. 保存后点击“刷新发布”

系统会校验：

- 代理池端口不能和面板端口冲突
- 代理池端口之间不能冲突

### 修改密码

1. 进入“系统设置”
2. 在面板设置中输入旧密码和新密码
3. 保存后会立即生效，并需要重新登录

### 重启系统

1. 进入“系统设置”
2. 页面底部点击“重启系统”
3. Docker 环境下建议必须开启 restart policy

### 限制代理池端口范围

1. 进入“系统设置”
2. 设置“代理池端口范围最小值”和“代理池端口范围最大值”
3. 保存后会立即生效
4. 后续创建或编辑代理池时，如果端口超出范围，后端会直接拒绝保存

行为说明：

- 程序会先持久化配置
- 再停止 Mihomo 子进程
- 最后退出主进程
- 由 Docker 的重启策略重新拉起

## 当前实现情况

### 已完成

- SQLite 自动建表与迁移
- bcrypt 密码存储与登录会话
- 四个管理页面和左侧固定导航
- 订阅 CRUD、同步、详情节点列表
- 手动节点 CRUD、解析、卡片操作
- 代理池 CRUD、成员管理、配置发布
- 双 Mihomo 配置文件生成
- 节点延迟测试队列
- 可选测速任务队列
- SSE 事件流
- Docker 单镜像部署

### 说明

- 本地没有 Mihomo 时，面板依然可用于开发和数据录入，但真实探测与转发不会生效
- 只要 Mihomo 二进制可用，程序会自动启动 `mihomo-prod` 和 `mihomo-probe`

## 关键环境变量

- `DATA_DIR`：数据目录
- `DB_PATH`：SQLite 文件路径
- `MIHOMO_BINARY`：Mihomo 二进制路径
- `PANEL_HOST`：面板监听地址
- `PANEL_PORT`：面板监听端口
- `PROD_CONTROLLER_ADDR`：生产实例 controller 地址
- `PROBE_CONTROLLER_ADDR`：探测实例 controller 地址
- `PROBE_MIXED_PORT`：探测实例 mixed 端口

## 目录结构

```text
cmd/app
internal/auth
internal/config
internal/db
internal/events
internal/mihomo
internal/models
internal/nodes
internal/pools
internal/probe
internal/settings
internal/subscriptions
internal/web
web/templates
web/static
deployments
```
