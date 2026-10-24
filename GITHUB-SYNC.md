# 多电脑开发同步

这是 2026-10-24 的源码同步快照。原电脑的原有分支和历史保留在本地；旧历史没有批量上传。

其他电脑使用项目清单中的克隆命令（已有仓库使用专门同步分支）。安装 README 中的依赖。

开发前 `git pull --ff-only`，开发后 `git add -A`、`git commit -m "描述修改"`、`git push`。
若工作区有未提交改动，先提交或 stash；不要使用强制推送来解决多电脑冲突。

环境变量、口令、数据库、录音、下载的模型/SDK压缩包和编译缓存不在同步范围内。
按已有 .env.example 或 README 在每台电脑配置本机环境。设备端应自行设置 Wi-Fi/MQTT 等运行参数。
如果项目依赖被排除的本地资源，请从原电脑或官方来源单独取得。

原电脑当前同步分支为 `private-sync-20261024`，推送目标为私有 remote `github-private`。
原 Git HEAD、暂存区索引、状态和 .gitignore 备份保存在 `.git/private-sync-backup-20261024/`。
