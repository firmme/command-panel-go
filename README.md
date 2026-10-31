# 命令面板 — Go + Wails

Windows 本地命令管理桌面应用，用于常驻命令、脚本和 SSH 隧道。支持系统托盘、命令归档、排序、自启和合并日志。

## 环境与开发

- Windows 10/11、WebView2 Runtime
- Go 1.27.2+、Node.js 18+
- Wails v3.0.0（构建脚本自动运行绑定生成器）

```powershell
npm --prefix frontend ci
.\scripts\build.ps1
```

当前实现仅支持 Windows，使用 Windows Job Object、DPAPI 和系统托盘，不提供 Unix 执行引擎。


## 版本更新与发布

程序启动后会检查 GitHub 的正式 Release；有新版本时可选择升级、稍后提醒或忽略此版本。顶部“检查更新”可手动检查，包括之前忽略的版本。升级会停止运行中的命令并重启程序；下载文件必须与 GitHub 提供的 SHA-256 摘要一致。忽略设置保存在用户配置目录的 `update-settings.json` 中。

仓库为私有时，需要在本机安装 GitHub CLI 并使用 `gh auth login` 登录有仓库读取权限的账号，或设置 `GH_TOKEN`。公开仓库无需登录。启动时网络故障不会打断使用，手动检查会显示错误。

发布时使用与 Release 标签一致的版本号构建，例如：

```powershell
.\scripts\build.ps1 -Version 1.2.3 -OutputName command-panel-windows-amd64.exe
```

创建 `v1.2.3` 正式 Release 并上传该文件（GitHub 生成资产 SHA-256 摘要）。更新器只接受匹配本机架构的 `command-panel-windows-<架构>.exe`；新版本号必须大于当前版本。不支持草稿和预发布。请勿将用户的 `commands.json`、日志或凭据加入 Release。

## 构建与验证

在项目根目录执行：

```powershell
.\scripts\build.ps1
```

脚本通过 Wails v3 生成绑定，再构建前端、嵌入应用图标/版本信息/Windows 清单并编译桌面程序，输出 `build/bin/command-panel.exe`。可用 `-OutputName command-panel-reviewed.exe` 指定其他文件名。

绑定生成不启动应用，也不会初始化用户数据或托盘。

```powershell
go test ./...
go vet ./...
npm --prefix frontend test
```

首次检出后先构建前端，再运行 Go 测试（入口会嵌入 `frontend/dist`）。测试覆盖保存失败回滚、损坏配置保护、归档排序、DPAPI、旧数据迁移、日志轮转与脱敏、进程树管理、停止失败以及前端异步请求竞争。

## 数据与迁移

配置固定存放于 `%APPDATA%/CommandPanel/commands.json`，日志位于同目录下的 `logs/`。切换启动目录不会改变数据位置；新命令未指定工作目录时，默认使用可执行文件所在目录。

首次运行时，如果目标配置尚不存在，会依次检查可执行文件目录和启动工作目录中的旧 `commands.json`，并迁移匹配命令的日志。已有目标配置不会被覆盖。旧命令的相对/默认工作目录会转换为原目录对应的绝对路径，保持运行含义。

原配置和日志保留在旧位置，其中可能含历史明文；确认迁移后可自行清理。迁移后的每份日志最多保留末尾 5 MiB，并对当前已知敏感值脱敏。

配置使用临时文件、刷盘和原子替换保存。保存失败会回滚内存变更并提示；配置损坏或无法解密时会保留原文件并阻止修改及自动执行。

仓库只跟踪 `commands.example.json`。项目根目录的旧 `commands.json` 已停止跟踪并被忽略，本机文件保留。

## 命令与敏感变量

命令支持名称、工作目录、引擎、备注、排序、归档和程序启动后自动运行。引擎可选 CMD、Windows PowerShell 5.1 或自动选择的 PowerShell；文件选择器按所选引擎生成正确的路径调用语法。

`${名称}` 变量值使用 Windows DPAPI 加密保存，只有原 Windows 用户能够解密；复制配置到其他账号或电脑时，需要重新配置变量值。替换只进行一遍，变量值中的其他占位符不会再次展开。

占位符仍属于 Shell 命令模板，需按照所选 Shell 的语法使用引号。日志头保留占位符；子进程输出中的已知变量原值会被替换为 `[REDACTED]`，包括跨读取块的值。经程序编码、拆分或转换后的内容无法保证识别，命令行参数仍可能被本机进程检查工具读取。

## 进程与日志

每条命令的 Shell 和后代进程由一个 Windows Job Object 管理；Shell 退出后，只要后代进程仍存活，命令仍显示运行中。停止操作必须确认整个任务退出；失败或超时会提示，并阻止删除/归档。退出程序时并行停止任务，同一用户会话只运行一个面板实例。

每条命令最多保留当前日志及两份备份，每份上限 5 MiB。日志面板按偏移增量读取，单次最多 256 KiB，界面最多保留约 256K 字符；轮转和清空会重置游标。面板折叠或窗口隐藏时暂停日志读取。

点击「自启」会在以下位置安装/卸载启动脚本，脚本直接调用带引号的可执行文件绝对路径：

```text
%APPDATA%/Microsoft/Windows/Start Menu/Programs/Startup/CommandPanelStartup.vbs
```

## 代码结构

- `app.go`：Wails 接口、生命周期和命令操作协调。
- `config.go`、`storage_windows.go`：配置事务、DPAPI、原子写入和迁移。
- `manager.go`、`process_windows.go`：进程状态与 Windows 进程树管理。
- `logs.go`：流式脱敏、日志轮转和有界增量读取。
- `tray.go`、`autostart.go`：系统托盘和开机自启。
- `main_bindings.go`：无用户数据副作用的绑定生成入口。
- `frontend/src/main.js`：事件、应用状态与轮询协调。
- `frontend/src/cards.js`、`form.js`、`log-panel.js`：卡片、表单和日志交互。
- `frontend/src/refresh-queue.js`：合并事件、操作和轮询刷新，避免请求重叠。
- `frontend/bindings/`：自动生成的桥接文件。
