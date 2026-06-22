# baidupcs-server

> 百度网盘命令行客户端 + HTTP REST API 服务  
> 基于 [qjfoidnh/BaiduPCS-Go](https://github.com/qjfoidnh/BaiduPCS-Go) 二次开发

[![Go](https://img.shields.io/badge/Go-1.22+-blue)](https://golang.org)
[![License](https://img.shields.io/badge/license-Apache2-green)](LICENSE)
[![Release](https://img.shields.io/github/v/release/MyGal-Dotcom/baidupcs-server)](https://github.com/MyGal-Dotcom/baidupcs-server/releases)

## 功能特色

- **命令行客户端**：完整的百度网盘 CLI，支持上传、下载、转存、分享等所有操作
- **HTTP REST API**：内置 Web API 服务器，任何语言均可通过 HTTP 调用网盘功能
- **本地分享数据库**：SQLite 持久化分享链接与文件关联，支持有效性检测与自动补链
- **安全加固**：Bearer Token 鉴权、速率限制、请求体限制、路径沙箱、安全响应头
- **仿 Android 端**：使用百度网盘 Android 客户端 UA 与 API，兼容性好
- **配置本地化**：配置文件默认生成在运行目录（`./pcs_config.json`），便于部署

---

## 目录

- [下载安装](#下载安装)
- [快速开始](#快速开始)
- [命令行用法](#命令行用法)
- [API 服务器](#api-服务器)
  - [启动](#启动)
  - [认证](#认证)
  - [接口列表](#接口列表)
- [分享数据库](#分享数据库)
- [安全说明](#安全说明)
- [构建](#构建)
- [致谢](#致谢)

---

## 下载安装

从 [Releases](https://github.com/MyGal-Dotcom/baidupcs-server/releases) 页面下载对应平台的二进制文件，解压后直接运行。

| 平台 | 文件 |
|------|------|
| Linux x64 | `baidupcs-server-*-linux-amd64.zip` |
| Windows x64 | `baidupcs-server-*-windows-amd64.zip` |
| macOS | `baidupcs-server-*-darwin-amd64.zip` |
| ARM64 | `baidupcs-server-*-linux-arm64.zip` |

---

## 快速开始

```bash
# 1. 登录百度账号（扫码或 BDUSS）
./baidupcs-server login

# 2. 列出网盘根目录
./baidupcs-server ls /

# 3. 下载文件
./baidupcs-server download /我的资源/movie.mp4

# 4. 启动 API 服务器（自动生成 Token）
./baidupcs-server server

# 5. 启动 API 服务器 + 分享数据库
./baidupcs-server server --db shares.db
```

> 配置文件 `pcs_config.json` 生成在**当前运行目录**。  
> 也可通过环境变量 `BAIDUPCS_GO_CONFIG_DIR` 指定其他目录：  
> `BAIDUPCS_GO_CONFIG_DIR=/etc/baidupcs ./baidupcs-server server`

---

## 命令行用法

```
baidupcs-server [全局选项] 命令 [命令选项] [参数]

全局选项：
  --verbose   启用调试输出

常用命令：
  login       登录百度账号
  logout      退出账号
  ls          列出目录
  cd          切换工作目录
  download    下载文件/目录（支持断点续传、多线程）
  upload      上传文件/目录
  transfer    转存他人分享链接到自己网盘
  share       管理分享链接
  mkdir       创建目录
  rm          删除文件/目录
  mv          移动/重命名
  cp          复制
  quota       查看网盘空间
  server      启动 Web API 服务器
  config      查看/修改配置

运行 baidupcs-server help <命令> 查看详细用法
```

---

## API 服务器

### 启动

```bash
# 最简启动（Token 自动生成并保存到配置文件）
./baidupcs-server server

# 指定端口
./baidupcs-server server -p 8080

# 指定 Token
./baidupcs-server server -t your_secret_token

# 启用分享数据库（开启 /api/db/* 接口）
./baidupcs-server server --db shares.db

# 完整示例
./baidupcs-server server -p 8080 -t your_secret_token --db shares.db
```

启动输出：
```
[API] 数据库: shares.db
[API] 服务已启动: http://0.0.0.0:8080
[API] Token: abcd****wxyz  (完整值请查看配置文件)
[API] 鉴权: Authorization: Bearer <token>
```

### 认证

所有接口必须携带 Token，二选一：

```bash
# 方式1：Header（推荐）
curl -H "Authorization: Bearer your_token" http://localhost:5299/api/user

# 方式2：URL 参数（会写入访问日志，不推荐用于生产）
curl "http://localhost:5299/api/user?token=your_token"
```

### 统一响应格式

```json
{
  "errno": 0,
  "errmsg": "success",
  "data": { ... }
}
```

错误时 `errno` 非零，`errmsg` 描述原因，`data` 为空。

---

### 接口列表

#### 用户与配额

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/user` | 当前登录用户信息（uid、用户名、工作目录） |
| GET | `/api/quota` | 网盘空间配额（`total`/`used`/`free`，单位字节） |

```bash
curl -H "Authorization: Bearer TOKEN" http://localhost:5299/api/quota
# {"errno":0,"errmsg":"success","data":{"free":30657030318798,"total":33634962636800,"used":2977932318002}}
```

#### 文件操作

| 方法 | 路径 | 说明 | 参数/Body |
|------|------|------|-----------|
| GET | `/api/ls` | 列出目录 | `?path=/我的资源` |
| GET | `/api/meta` | 文件/目录元信息 | `?paths=/a,/b`（逗号分隔） |
| GET | `/api/search` | 搜索文件 | `?path=/&keyword=视频&recurse=1` |
| POST | `/api/mkdir` | 创建目录 | `{"path":"/新目录"}` |
| POST | `/api/rm` | 删除（支持批量） | `{"paths":["/file.txt","/dir"]}` |
| POST | `/api/rename` | 重命名 | `{"from":"/old.txt","to":"/new.txt"}` |
| POST | `/api/mv` | 移动（支持批量） | `{"from_to":[{"from":"/src","to":"/dst"}]}` |
| POST | `/api/cp` | 复制（支持批量） | 同移动格式 |

`ls` 响应示例：
```json
{
  "errno": 0,
  "errmsg": "success",
  "data": [
    {
      "fs_id": 700833900701678,
      "path": "/工具",
      "filename": "工具",
      "size": 0,
      "is_dir": true,
      "ctime": 1762695319,
      "mtime": 1775448855
    }
  ]
}
```

#### 下载直链

| 方法 | 路径 | 说明 | 参数 |
|------|------|------|------|
| GET | `/api/locate` | 获取文件下载直链 | `?path=/file.mp4`，加 `&from_pan=1` 走网盘首页接口 |

```bash
curl -H "Authorization: Bearer TOKEN" \
  "http://localhost:5299/api/locate?path=/工具/galbot.zip"
# {"errno":0,"errmsg":"success","data":{"urls":["https://bjbgp01.baidupcs.com/..."]}}
```

#### 转存分享链接

```
POST /api/transfer
```

```json
{
  "link": "https://pan.baidu.com/s/1xxxxxxxxx",
  "pwd": "abcd",
  "saveto": "/我的资源",
  "collect": false
}
```

| 字段 | 说明 |
|------|------|
| `link` | 百度网盘分享链接（必填） |
| `pwd` | 提取码（必填，4位） |
| `saveto` | 保存目标目录，默认当前工作目录 |
| `collect` | 多文件时是否归集到同名子目录 |

#### 异步下载到本地

```
POST /api/download
```

```json
{
  "paths": ["/我的资源/movie.mp4"],
  "saveto": "/home/user/Downloads/subdir",
  "parallel": 4
}
```

> `saveto` 必须在配置文件 `savedir` 目录下，默认为 `Downloads/`。

响应立即返回 `task_id`，下载在后台执行：
```json
{"errno":0,"errmsg":"success","data":{"task_id":"1782113304648-1"}}
```

#### 分享管理

| 方法 | 路径 | 说明 | Body |
|------|------|------|------|
| POST | `/api/share/set` | 创建分享链接 | `{"paths":["/file"],"period":0,"pwd":""}` |
| POST | `/api/share/cancel` | 取消分享 | `{"ids":[123456789]}` |
| GET | `/api/share/list` | 列出我的分享 | `?page=1` |

- `period`：有效期（天），0 为永久
- `pwd`：提取码，留空自动生成 4 位

`share/list` 响应：
```json
{
  "data": [
    {
      "share_id": 17295181802,
      "link": "https://pan.baidu.com/s/1o8XoXUnrmnRehk9W6Q5dLQ",
      "passwd": "d354",
      "path": "/转存/file.txt",
      "view_count": 0,
      "expire_at": "永久"
    }
  ]
}
```

#### 异步任务管理

下载等长耗时操作异步执行，可通过任务接口查询进度：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/tasks` | 列出所有任务 |
| GET | `/api/task?id=<task_id>` | 查询单个任务 |

任务状态：`pending` → `running` → `done` / `failed`

---

## 分享数据库

启动时加 `--db shares.db` 开启，提供分享链接的本地持久化、有效性检测与自动补链功能。

> 未指定 `--db` 时，所有 `/api/db/*` 接口返回 `errno: 503`。

### 主键计算

每条分享记录的主键 `id` = SHA-256(featureStr) 前 8 字节转十六进制（16位），同一分享链接始终对应同一 ID。

### 接口

| 方法 | 路径 | 说明 | Body/参数 |
|------|------|------|-----------|
| POST | `/api/db/share/save` | 保存分享（自动获取文件列表） | `{"link":"...","pwd":"..."}` |
| GET | `/api/db/shares` | 列出所有保存的分享 | — |
| GET | `/api/db/share?id=` | 获取分享详情（含文件列表） | — |
| DELETE | `/api/db/share?id=` | 删除分享记录 | — |
| POST | `/api/db/share/check` | 检测单条链接是否失效 | `{"id":"..."}` 或 `{"link":"..."}` |
| POST | `/api/db/share/check-all` | 批量异步检测所有分享（返回 task_id） | — |
| POST | `/api/db/share/relink` | 补链（失效后重新分享） | `{"id":"...","save_path":"/网盘目录"}` |
| GET | `/api/db/files/search?keyword=` | 搜索所有保存分享中的文件 | — |

#### 补链逻辑

1. 读取失效分享的文件列表（从 DB）
2. 用第一个文件名在指定目录搜索网盘
3. 找到后自动创建新分享链接
4. 更新 DB 为新链接

```bash
curl -X POST -H "Authorization: Bearer TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"fceb3559df5a8498","save_path":"/我的资源"}' \
  http://localhost:5299/api/db/share/relink
```

---

## 安全说明

| 机制 | 实现 |
|------|------|
| Bearer Token 鉴权 | `Authorization: Bearer <token>` 头，常量时间比较防时序攻击 |
| 暴力枚举防护 | 60s 内失败 10 次 → 429，成功后重置计数 |
| 请求体限制 | 最大 1MB，防 OOM |
| CORS 阻断 | 拒绝 OPTIONS 预检（403），防 CSRF |
| 下载路径沙箱 | `saveto` 只允许 `SaveDir` 子目录，防任意文件写入 |
| 错误信息脱敏 | 不向调用方透传 Baidu 内部错误码 |
| 安全响应头 | `X-Content-Type-Options: nosniff` 等 5 项 |
| Token 日志掩码 | 启动日志只显示头尾各 4 位 |

**注意事项：**

- 本服务仅提供 HTTP，不内置 TLS。生产环境建议用 nginx / Caddy 做 HTTPS 终止
- 配置文件 `pcs_config.json` 包含 BDUSS 和 Token，权限建议设为 `600`：`chmod 600 pcs_config.json`
- URL 参数 `?token=` 会出现在服务器访问日志，生产环境只用 `Authorization` 头

---

## 构建

需要 Go 1.22+。

```bash
git clone https://github.com/MyGal-Dotcom/baidupcs-server.git
cd baidupcs-server

# 构建当前平台
go build -o baidupcs-server .

# 交叉编译（以 Linux amd64 为例）
GOOS=linux GOARCH=amd64 go build -o baidupcs-server-linux-amd64 .

# 构建多平台（使用 build.sh）
bash build.sh
```

---

## 致谢

本项目基于以下开源工作：

- [iikira/BaiduPCS-Go](https://github.com/iikira/BaiduPCS-Go) — 原始项目
- [qjfoidnh/BaiduPCS-Go](https://github.com/qjfoidnh/BaiduPCS-Go) — 直接上游，v3.x/v4.x 维护版本

**主要增强内容（相对上游）：**

- 新增 HTTP REST API 服务器（`server` 子命令）
- Bearer Token 鉴权 + 多层安全中间件
- SQLite 分享数据库（持久化、有效性检测、自动补链）
- 配置文件默认生成在运行目录
- JSON 响应 snake_case 格式化 + 内部字段脱敏

---

*Licensed under [Apache License 2.0](LICENSE)*
