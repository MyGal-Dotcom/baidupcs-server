// Package pcsserver 提供百度网盘 Web API 服务
package pcsserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil"
)

// Resp 统一 JSON 响应体
type Resp struct {
	Errno  int         `json:"errno"`
	Errmsg string      `json:"errmsg"`
	Data   interface{} `json:"data,omitempty"`
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, data interface{}) {
	writeJSON(w, Resp{Errno: 0, Errmsg: "success", Data: data})
}

func fail(w http.ResponseWriter, errno int, msg string) {
	writeJSON(w, Resp{Errno: errno, Errmsg: msg})
}

// maskToken 只显示 token 头尾各 4 字符，中间用 * 替代
func maskToken(t string) string {
	if len(t) <= 8 {
		return strings.Repeat("*", len(t))
	}
	return t[:4] + strings.Repeat("*", len(t)-8) + t[len(t)-4:]
}

func registerRoutes(mux *http.ServeMux) {
	// 网盘基础操作
	mux.HandleFunc("/api/user", handleUser)
	mux.HandleFunc("/api/quota", handleQuota)
	mux.HandleFunc("/api/ls", handleLs)
	mux.HandleFunc("/api/meta", handleMeta)
	mux.HandleFunc("/api/search", handleSearch)
	mux.HandleFunc("/api/mkdir", handleMkdir)
	mux.HandleFunc("/api/rm", handleRm)
	mux.HandleFunc("/api/rename", handleRename)
	mux.HandleFunc("/api/mv", handleMv)
	mux.HandleFunc("/api/cp", handleCp)
	mux.HandleFunc("/api/transfer", handleTransfer)
	mux.HandleFunc("/api/locate", handleLocate)
	mux.HandleFunc("/api/download", handleDownload)
	mux.HandleFunc("/api/share/set", handleShareSet)
	mux.HandleFunc("/api/share/cancel", handleShareCancel)
	mux.HandleFunc("/api/share/list", handleShareList)
	// 异步任务
	mux.HandleFunc("/api/tasks", handleTasks)
	mux.HandleFunc("/api/task", handleTask)
	// 自检
	mux.HandleFunc("/api/selftest", handleSelfTest)
	// 分享数据库
	mux.HandleFunc("/api/db/share/save", handleDBSaveShare)
	mux.HandleFunc("/api/db/share/check", handleDBCheckShare)
	mux.HandleFunc("/api/db/share/check-all", handleDBCheckAll)
	mux.HandleFunc("/api/db/share/relink", handleDBRelink)
	mux.HandleFunc("/api/db/share", handleDBShareDispatch)
	mux.HandleFunc("/api/db/shares", handleDBListShares)
	mux.HandleFunc("/api/db/files/search", handleDBSearchFiles)
}

// handleDBShareDispatch 根据 HTTP 方法路由 /api/db/share
func handleDBShareDispatch(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		handleDBGetShare(w, r)
	case http.MethodDelete:
		handleDBDeleteShare(w, r)
	default:
		fail(w, 405, "method not allowed")
	}
}

// Start 启动 Web API 服务器。dbPath 非空时初始化 SQLite 数据库，否则 /api/db/* 接口返回 503。
// runTest 为 true 时启动后台自检循环（每 24 小时执行一次）。
func Start(addr, dbPath string, runTest bool) error {
	cfg := pcsconfig.Config

	if cfg.APIToken == "" {
		cfg.APIToken = pcsutil.GenerateRandomString(32)
		if err := cfg.Save(); err != nil {
			fmt.Printf("[API] 警告: 保存 token 失败: %s\n", err)
		}
	}

	if addr == "" {
		addr = cfg.APIAddr
	}
	if addr == "" {
		addr = ":5299"
	}

	if dbPath != "" {
		if err := InitDB(dbPath); err != nil {
			return fmt.Errorf("init database: %w", err)
		}
		fmt.Printf("[API] 数据库: %s\n", dbPath)
	}

	mux := http.NewServeMux()
	registerRoutes(mux)

	fmt.Printf("[API] 服务已启动: http://0.0.0.0%s\n", addr)
	fmt.Printf("[API] Token: %s  (完整值请查看配置文件)\n", maskToken(cfg.APIToken))
	fmt.Printf("[API] 鉴权: Authorization: Bearer <token>\n\n")

	if runTest {
		StartSelfTestLoop()
	} else {
		fmt.Println("[自检] 已通过 --no-test 禁用")
	}

	handler := securityHeaders(limitBody(secureAuth(mux, cfg.APIToken)))
	return http.ListenAndServe(addr, handler)
}
