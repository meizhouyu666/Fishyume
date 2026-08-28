# dsh-fishyume

Fishyume 的 Web 控制台，同时提供两种形态：

1. **DSH 插件**（本包主要形态）：把 Fishyume Team/Run 控制台内嵌进 DeepSeek
   Harness 的 Web GUI，通过 `shell.overlay` 浮层展示，无需独立端口。
2. **独立 Web 客户端**（兼容保留）：`fishyume-web` 命令起一个认证 loopback
   sidecar 并打开浏览器，供不安装 DSH 的场景使用。

两种形态共用同一份 `gateway`（JSON-RPC over HTTP，Bearer token + loopback）和
同一个 client SPA，只是宿主不同（DSH web server vs 独立 Node 进程）。

## 安装（DSH 插件）

```powershell
# 从本地源码安装（开发）
dsh plugin --profile web add E:\path\to\Fishyume\fishyume-web

# 重启目标 profile 后，右上角 shell.overlay 出现 "Fishyume console" 面板
```

插件是双面的：

- **host 面**（`src/plugin.ts`）：在共享 `webServer` 上注册
  `/plugins/dsh-fishyume/token`、`/plugins/dsh-fishyume/api/rpc`（网关）和
  `/plugins/dsh-fishyume/`（静态 client）。
- **client 面**（`src/client/plugin.tsx`）：向 `shell.overlay` 注册面板，面板用
  iframe 加载由 host 面提供的 client SPA。

Engine 二进制解析沿用 Fishyume 的优先级：`FISHYUME_ENGINE_PATH` → 安装的
`fishyume-engine-win32-x64` 包 → 开发 checkout 的 `wf-engine/wf-engine.exe`。

## 独立客户端（保留）

```powershell
fishyume-web            # 起 loopback sidecar 并打开浏览器
fishyume-web --no-open  # 只打印 launch URL，不自动打开
```

## 构建

```powershell
npm run build            # dist/（独立 sidecar + SPA）+ lib/（插件 host/client）
npm run typecheck        # 仅类型检查
```

`lib/plugin.js`（host）和 `lib/client.js`（client，包成
`window.__ModuleLoader__.load`）是插件入口，由 `scripts/build-plugin.mjs` 产出；
构建产物（`lib/`、`dist/`）不入 Git。

## 状态

- P0 完成：插件骨架 + 构建 + `dsh plugin add` + `--dump-config` 出层 + host 面加载。
- 待验证：在真实 web profile 中刷新页面确认 iframe 面板渲染（需浏览器）。
- 待做：`web.open` 联动聚焦 team/run、Git 分发验证。
