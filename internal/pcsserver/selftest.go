package pcsserver

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

const (
	selfTestLink       = "https://pan.baidu.com/s/1pEwltT7wM_275bEhfFmaIQ"
	selfTestPwd        = "94au"
	selfTestFeatureStr = "1pEwltT7wM_275bEhfFmaIQ"
	selfTestNetdiskDir = "/baidupcs-server-selftest"
	selfTestReportFile = "selftest_report.log"
	selfTestInterval   = 24 * time.Hour
)

// SelfTestReport 单次自检报告
type SelfTestReport struct {
	Time    time.Time
	OK      bool
	Mode    string // "full" 或 "degraded"
	Steps   []string
	Message string
}

func (r *SelfTestReport) log(step string) {
	r.Steps = append(r.Steps, step)
	fmt.Println("[自检]", step)
}

func (r *SelfTestReport) fail(reason string) {
	r.Message = reason
	r.OK = false
	fmt.Printf("[自检] ❌ 失败: %s\n", reason)
}

// RunSelfTest 根据是否有 STOKEN 选择完整或降级测试
func RunSelfTest() *SelfTestReport {
	report := &SelfTestReport{Time: time.Now(), OK: true}
	report.log("开始自检 " + report.Time.Format("2006-01-02 15:04:05"))

	user := pcsconfig.Config.ActiveUser()
	if user == nil || user.BDUSS == "" {
		report.fail("未登录百度账号，请先运行 baidupcs-server login")
		writeReport(report)
		return report
	}

	hasStoken := user.STOKEN != "" || strings.Contains(user.COOKIES, "STOKEN=")
	if hasStoken {
		report.Mode = "full"
		report.log("模式: 完整测试（转存 + 下载验证）")
		runFullTest(report)
	} else {
		report.Mode = "degraded"
		report.log("模式: 降级测试（无 STOKEN，跳过转存，测试 API 连通 + 下载直链）")
		report.log("提示: 运行 baidupcs-server login 完整登录后可启用完整测试")
		runDegradedTest(report)
	}

	writeReport(report)
	return report
}

// ── 完整测试（需要 STOKEN）────────────────────────────────────────────────

func runFullTest(report *SelfTestReport) {
	p := pcsconfig.Config.ActiveUserBaiduPCS()

	// 步骤1：转存分享链接
	report.log(fmt.Sprintf("步骤1: 转存分享链接 %s?pwd=%s", selfTestLink, selfTestPwd))
	p.Mkdir(selfTestNetdiskDir)

	transferMu.Lock()
	tokens := p.AccessSharePage(selfTestFeatureStr, true)
	if tokens["ErrMsg"] != "0" {
		transferMu.Unlock()
		report.fail("访问分享页失败: " + tokens["ErrMsg"])
		return
	}

	verifyURL := p.GenerateShareQueryURL("verify", map[string]string{
		"shareid":    tokens["shareid"],
		"time":       strconv.FormatInt(time.Now().UnixMilli(), 10),
		"clienttype": "1",
		"uk":         tokens["share_uk"],
	}).String()

	res := p.PostShareQuery(verifyURL, selfTestLink, map[string]string{
		"pwd":       selfTestPwd,
		"vcode":     "null",
		"vcode_str": "null",
		"bdstoken":  tokens["bdstoken"],
	})
	if res["ErrMsg"] != "0" {
		transferMu.Unlock()
		report.fail("提取码验证失败: " + res["ErrMsg"])
		return
	}

	p.UpdatePCSCookies(true)
	tokens = p.AccessSharePage(selfTestFeatureStr, false)
	if tokens["ErrMsg"] != "0" {
		transferMu.Unlock()
		report.fail("二次访问分享页失败: " + tokens["ErrMsg"])
		return
	}

	featureMap := map[string]string{
		"bdstoken": tokens["bdstoken"],
		"root":     "1", "web": "5",
		"app_id":   baidupcs.PanAppID,
		"shorturl": selfTestFeatureStr[1:],
		"channel":  "chunlei",
	}
	queryURL := p.GenerateShareQueryURL("list", featureMap).String()
	transMetas := p.ExtractShareInfo(queryURL, tokens["shareid"], tokens["share_uk"], tokens["bdstoken"])
	if transMetas["ErrMsg"] != "success" {
		transferMu.Unlock()
		report.fail("获取分享文件列表失败: " + transMetas["ErrMsg"])
		return
	}

	transMetas["path"] = selfTestNetdiskDir
	transMetas["referer"] = "https://pan.baidu.com/s/" + selfTestFeatureStr
	p.UpdatePCSCookies(true)
	transferResp := p.GenerateRequestQuery("POST", transMetas)
	transferMu.Unlock()

	if transferResp["ErrNo"] != "0" {
		if transferResp["ErrNo"] == "9" {
			report.log("文件已存在，跳过转存（重复测试）")
		} else {
			report.fail("转存失败: " + transferResp["ErrMsg"])
			return
		}
	} else {
		report.log("✓ 转存成功: " + transferResp["filename"])
	}

	// 步骤2：找到转存文件
	report.log("步骤2: 列出网盘测试目录")
	testFile := findFirstFile(p, selfTestNetdiskDir)
	if testFile == nil {
		report.fail("测试目录中没有找到文件")
		return
	}
	report.log(fmt.Sprintf("✓ 测试文件: %s (%.1f KB)", testFile.Filename, float64(testFile.Size)/1024))

	// 步骤3：获取直链并验证
	if !locateAndProbe(report, p, testFile.Path, "步骤3") {
		return
	}

	// 步骤4：清理网盘
	report.log("步骤4: 清理网盘测试文件")
	if err := p.Remove(testFile.Path); err != nil {
		report.log("⚠ 清理失败（不影响结果）: " + err.Error())
	} else {
		report.log("✓ 已删除")
	}
	remaining, _ := p.FilesDirectoriesList(selfTestNetdiskDir, baidupcs.DefaultOrderOptions)
	if len(remaining) == 0 {
		p.Remove(selfTestNetdiskDir)
	}

	report.log("自检完成 ✅ 功能正常")
}

// ── 降级测试（无需 STOKEN）────────────────────────────────────────────────

func runDegradedTest(report *SelfTestReport) {
	p := pcsconfig.Config.ActiveUserBaiduPCS()

	// 步骤1：配额检查（验证登录状态）
	report.log("步骤1: 验证登录状态（配额查询）")
	total, used, pcsErr := p.QuotaInfo()
	if pcsErr != nil {
		report.fail("配额查询失败（可能 BDUSS 已过期）: " + pcsErr.Error())
		return
	}
	report.log(fmt.Sprintf("✓ 网盘配额正常: 已用 %.1f GB / 共 %.1f GB",
		float64(used)/1e9, float64(total)/1e9))

	// 步骤2：列出根目录，找一个可用文件
	report.log("步骤2: 列出根目录，寻找测试文件")
	testFile := findAnyFile(p)
	if testFile == nil {
		// 没有文件也算通过（网盘可能是空的），跳过下载测试
		report.log("⚠ 网盘中未找到可用文件，跳过下载测试")
		report.log("自检完成 ✅ 连通性正常（无文件可测试下载）")
		return
	}
	report.log(fmt.Sprintf("✓ 找到测试文件: %s", testFile.Path))

	// 步骤3：获取直链并验证
	if !locateAndProbe(report, p, testFile.Path, "步骤3") {
		return
	}

	report.log("自检完成 ✅ 功能正常")
}

// ── 共用工具 ──────────────────────────────────────────────────────────────

// locateAndProbe 获取下载直链并发送 Range 请求验证可用性
func locateAndProbe(report *SelfTestReport, p *baidupcs.BaiduPCS, pcspath, label string) bool {
	report.log(label + ": 获取下载直链")
	urlInfo, pcsErr := p.LocateDownload(pcspath)
	if pcsErr != nil || urlInfo == nil || len(urlInfo.URLs) == 0 {
		report.fail("获取下载直链失败")
		return false
	}
	var downloadURL string
	for _, u := range urlInfo.URLs {
		if u.Encrypt == 0 {
			downloadURL = u.URL
			break
		}
	}
	if downloadURL == "" {
		report.fail("无可用下载链接（均为加密链接）")
		return false
	}
	report.log("✓ 直链获取成功，发送 Range 请求验证（前 1KB）")
	n, err := probeDownloadURL(downloadURL)
	if err != nil {
		report.fail("直链验证失败: " + err.Error())
		return false
	}
	report.log(fmt.Sprintf("✓ 直链可用，收到 %d 字节", n))
	return true
}

// findFirstFile 在指定目录找第一个文件
func findFirstFile(p *baidupcs.BaiduPCS, dir string) *baidupcs.FileDirectory {
	files, err := p.FilesDirectoriesList(dir, baidupcs.DefaultOrderOptions)
	if err != nil {
		return nil
	}
	for _, f := range files {
		if !f.Isdir && f.Size > 0 {
			return f
		}
	}
	return nil
}

// findAnyFile 在整个网盘中找第一个可用文件（广度优先，最多扫两层）
func findAnyFile(p *baidupcs.BaiduPCS) *baidupcs.FileDirectory {
	roots, err := p.FilesDirectoriesList("/", baidupcs.DefaultOrderOptions)
	if err != nil {
		return nil
	}
	for _, item := range roots {
		if !item.Isdir && item.Size > 0 {
			return item
		}
	}
	// 第二层
	for _, item := range roots {
		if item.Isdir {
			if f := findFirstFile(p, item.Path); f != nil {
				return f
			}
		}
	}
	return nil
}

// probeDownloadURL 只请求前 1KB 验证直链可用
func probeDownloadURL(rawURL string) (int64, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", baidupcs.NetdiskUA)
	req.Header.Set("Referer", "https://pan.baidu.com/")
	req.Header.Set("Range", "bytes=0-1023")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	n, err := io.Copy(io.Discard, resp.Body)
	return n, err
}

// writeReport 追加写入报告文件
func writeReport(r *SelfTestReport) {
	lines := []string{
		strings.Repeat("─", 60),
		fmt.Sprintf("时间: %s  模式: %s", r.Time.Format("2006-01-02 15:04:05"), r.Mode),
	}
	for _, s := range r.Steps {
		lines = append(lines, "  "+s)
	}
	if r.OK {
		lines = append(lines, "结果: 功能正常")
	} else {
		lines = append(lines, "结果: ❌ "+r.Message)
	}
	lines = append(lines, "")

	content := strings.Join(lines, "\n") + "\n"
	reportPath := path.Join(pcsconfig.GetConfigDir(), selfTestReportFile)
	f, err := os.OpenFile(reportPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("[自检] 警告: 无法写入报告 %s: %s\n", reportPath, err)
		return
	}
	defer f.Close()
	f.WriteString(content)
	fmt.Printf("[自检] 报告已写入: %s\n", reportPath)
}

// StartSelfTestLoop 启动自检循环
func StartSelfTestLoop() {
	go func() {
		fmt.Println("[自检] 将在 5 秒后执行首次自检（使用 --no-test 禁用）")
		time.Sleep(5 * time.Second)
		RunSelfTest()
		ticker := time.NewTicker(selfTestInterval)
		defer ticker.Stop()
		for t := range ticker.C {
			fmt.Printf("[自检] 定时触发 %s\n", t.Format("2006-01-02 15:04:05"))
			RunSelfTest()
		}
	}()
}
