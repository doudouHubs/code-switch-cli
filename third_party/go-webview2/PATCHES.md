# CodeSwitch WebView2 patch

本目录基于 `github.com/wailsapp/go-webview2 v1.0.22`，仅保留当前 Wails
`v3.0.0-alpha.38` 实际依赖的包。原始许可证见 `LICENSE`。

`pkg/edge/chromium.go` 回移了后续官方模块
`github.com/wailsapp/wails/webview2 v1.0.27` 的运行期错误恢复策略：

- 非字符串页面消息、附加对象读取失败和消息回写失败只记录并丢弃当前消息。
- resize、navigation、Eval、focus、zoom 和 DevTools 的瞬时 COM 错误不再调用
  `errorCallback`，避免其内部 `os.Exit(1)` 直接结束宿主进程。
- WebView2 环境或 controller 无法创建等初始化错误继续保留 fail-fast 行为。

该本地替换应在项目完成 Wails 整体升级、且上游依赖至少包含
`github.com/wailsapp/wails/webview2 v1.0.26` 后删除。
