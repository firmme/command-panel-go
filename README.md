# 命令面板 - Go + Wails 版本

一个纯本地轻量命令管理桌面应用，用来管理常用长驻命令、脚本、SSH 隧道等任务。

使用 Go + Wails v2 构建，原生桌面窗口 + 系统托盘，无需浏览器。

## 主要特性

- 纯本地运行，不依赖远程服务
- 原生桌面窗口，暗色主题
- 系统托盘常驻，托盘图标显示任务状态
- 支持新增、编辑、删除命令
- 支持归档命令，归档后不参与监控
- 命令支持可选名称、注释、工作目录
- 支持拖动排序
- 每条命令可配置程序启动 5 秒后自动运行
- 启动命令后非阻塞运行
- stdout 和 stderr 合并写入独立日志文件
- 底部日志面板，支持展开、收起
- 停止命令时清理进程树，Windows 下使用 `taskkill /T /F` 兜底
- 支持安装和卸载 Windows 开机自启

## 环境要求

- Go 1.23+
- Node.js 18+
- Wails CLI v2 (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)

## 开发

```powershell
# 安装前端依赖
cd frontend
npm install
cd ..

# 开发模式运行
wails dev
```

## 构建

```powershell
wails build
```

构建产物在 `build/bin/command-panel.exe`。

## 项目结构

```
command-panel/
├── main.go           # Wails 应用入口
├── app.go            # App 结构体，绑定到前端的所有方法
├── model.go          # 数据模型 (Command, RunState)
├── config.go         # commands.json 配置读写
├── manager.go        # 进程管理（启动/停止/日志）
├── tray.go           # 系统托盘逻辑
├── autostart.go      # Windows 开机自启管理
├── commands.json     # 命令配置文件（运行时生成）
├── logs/             # 命令日志目录（运行时生成）
├── build/            # 构建输出
│   └── bin/
│       └── command-panel.exe
└── frontend/
    ├── index.html    # 主页面
    ├── src/
    │   ├── main.js   # 前端逻辑
    │   └── style.css # 样式
    └── wailsjs/      # Wails 自动生成的 JS 绑定
```

## 呑令配置 (commands.json)

```json
{
  "commands": [
    {
      "id": "cmd-example01",
      "name": "隧道A",
      "command": "ssh -N -D 8080 user@host",
      "cwd": "",
      "note": "本地 SOCKS5 代理",
      "autoStart": false,
      "order": 0,
      "archived": false,
      "archivedAt": "",
      "createdAt": "2026-05-07T13:00:00+08:00",
      "updatedAt": "2026-05-07T13:00:00+08:00"
    }
  ]
}
```

## 日志格式

每个命令的 stdout 和 stderr 合并写入 `logs/<命令id>.log`：

```text
2026-05-07 13:00:00  START - name=隧道A
2026-05-07 13:00:00  CWD - D:\code\command-panel
2026-05-07 13:00:00  COMMAND - ssh -N -D 8080 user@host
2026-05-07 13:00:01  OUT - server started
2026-05-07 13:05:30  EXIT code=0 - pid=12345
```

## 开机自启

点击导航栏"自启"按钮安装/卸载 Windows 开机自启。

安装后会在 Startup 目录创建启动脚本：

```text
%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\CommandPanelStartup.vbs
```
