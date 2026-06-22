# 贡献指南

## 分支策略

| 分支 | 说明 |
|------|------|
| `main` | 稳定分支，仅接受来自 `develop` 的 PR 和紧急 hotfix |
| `develop` | 日常开发分支，新功能和修复都提交到这里 |
| `feature/xxx` | 功能分支，从 `develop` 切出，完成后 PR 回 `develop` |
| `fix/xxx` | Bug 修复分支，从 `develop` 切出 |
| `hotfix/xxx` | 紧急修复，从 `main` 切出，合并到 `main` 和 `develop` |

## 开发流程

```bash
# 1. Fork 并克隆仓库
git clone https://github.com/MyGal-Dotcom/baidupcs-server.git
cd baidupcs-server

# 2. 从 develop 创建功能分支
git checkout develop
git checkout -b feature/my-feature

# 3. 开发并提交
git add .
git commit -m "feat: 添加xxx功能"

# 4. 推送并创建 PR（目标分支：develop）
git push origin feature/my-feature
```

## Commit 规范

格式：`<type>: <描述>`

| type | 含义 |
|------|------|
| `feat` | 新功能 |
| `fix` | Bug 修复 |
| `docs` | 文档 |
| `refactor` | 重构 |
| `perf` | 性能优化 |
| `ci` | CI/CD 配置 |

## 发布流程

1. `develop` → `main` PR（需要 review）
2. 合并后在 `main` 打 tag：`git tag vX.Y.Z && git push origin vX.Y.Z`
3. GitHub Actions 自动构建多平台二进制并发布 Release

## 编译

```bash
go build -o baidupcs-server .
```

需要 Go 1.22+。
