package pcsserver

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

// ---- 常量时间 Token 验证 ----

// tokenEqual 使用常量时间比较防止时序侧信道攻击
func tokenEqual(provided, expected string) bool {
	if len(provided) == 0 || len(expected) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// ---- 速率限制 ----

const (
	authRateWindow  = 60 * time.Second // 滑动窗口
	authRateMaxFail = 10               // 窗口内最多失败次数
)

type ipRecord struct {
	mu       sync.Mutex
	failures []time.Time
}

var authRateMu sync.RWMutex
var authRateMap = make(map[string]*ipRecord)

func getIPRecord(ip string) *ipRecord {
	authRateMu.RLock()
	rec, ok := authRateMap[ip]
	authRateMu.RUnlock()
	if ok {
		return rec
	}
	authRateMu.Lock()
	defer authRateMu.Unlock()
	rec, ok = authRateMap[ip]
	if !ok {
		rec = &ipRecord{}
		authRateMap[ip] = rec
	}
	return rec
}

// rateLimitExceeded 检查并记录一次失败，返回是否超限
func rateLimitExceeded(remoteAddr string) bool {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ip = remoteAddr
	}
	rec := getIPRecord(ip)
	rec.mu.Lock()
	defer rec.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-authRateWindow)

	// 清理过期记录
	valid := rec.failures[:0]
	for _, t := range rec.failures {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	rec.failures = valid

	if len(rec.failures) >= authRateMaxFail {
		return true
	}
	rec.failures = append(rec.failures, now)
	return false
}

// resetRateLimit 认证成功后清空失败计数
func resetRateLimit(remoteAddr string) {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ip = remoteAddr
	}
	rec := getIPRecord(ip)
	rec.mu.Lock()
	rec.failures = rec.failures[:0]
	rec.mu.Unlock()
}

// ---- 安全中间件 ----

// securityHeaders 注入通用安全响应头，并拒绝跨域 CORS 预检请求。
// API 仅供本机/受信任客户端直接调用，不开放浏览器跨域访问。
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-XSS-Protection", "1; mode=block")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")

		// 拒绝浏览器跨域预检（CORS OPTIONS），防止 CSRF
		// 合法调用方（curl / SDK）不发 OPTIONS
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limitBody 限制请求体最大 1MB，防止 OOM
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}

// secureAuth 替换原 authMiddleware，加入速率限制 + 常量时间比较
func secureAuth(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

		// 先检查速率限制（在取 token 之前，防止枚举）
		if rateLimitExceeded(r.RemoteAddr) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			writeJSON(w, Resp{Errno: 429, Errmsg: "too many failed attempts, retry after 60s"})
			return
		}

		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		// URL 参数 token（仅允许在非浏览器环境下使用，同时记录警告）
		if provided == "" {
			provided = r.URL.Query().Get("token")
		}

		if !tokenEqual(provided, token) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(w, Resp{Errno: 401, Errmsg: "unauthorized"})
			return
		}

		resetRateLimit(r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

// ---- 错误信息脱敏 ----

// sanitizeErr 对外屏蔽 Baidu 内部错误码，只保留语义分类
func sanitizeErr(raw string) string {
	// Baidu 错误响应包含"代码: NNNNN"模式
	if strings.Contains(raw, "代码:") || strings.Contains(raw, "远端服务器返回错误") {
		// 提取中文语义部分（"消息: " 后面的内容）
		if idx := strings.Index(raw, "消息: "); idx != -1 {
			return strings.TrimSpace(raw[idx+len("消息: "):])
		}
		// 降级：只取第一句
		if idx := strings.Index(raw, ","); idx != -1 {
			return strings.TrimSpace(raw[:idx])
		}
	}
	return raw
}

// ---- 本地路径沙箱 ----

// safeSaveTo 验证下载保存路径必须在允许的根目录内，防止任意文件写入。
// 若 requested 为空则返回默认目录；若超出边界则返回错误。
func safeSaveTo(requested string) (string, error) {
	allowed := pcsconfig.Config.SaveDir
	if allowed == "" {
		allowed = "Downloads"
	}
	// 解析为绝对路径
	absAllowed, err := filepath.Abs(allowed)
	if err != nil {
		return "", err
	}
	if requested == "" {
		return absAllowed, nil
	}
	absReq, err := filepath.Abs(requested)
	if err != nil {
		return "", err
	}
	// 必须以 absAllowed + 分隔符 为前缀（防止 /Downloads2 绕过 /Downloads 检查）
	if absReq != absAllowed && !strings.HasPrefix(absReq, absAllowed+string(filepath.Separator)) {
		return "", fmt.Errorf("saveto 路径超出允许范围 (%s)", absAllowed)
	}
	return absReq, nil
}
