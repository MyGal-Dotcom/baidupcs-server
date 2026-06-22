package pcsserver

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

const (
	selfTestLink       = "https://pan.baidu.com/s/1pEwltT7wM_275bEhfFmaIQ"
	selfTestPwd        = "94au"
	selfTestNetdiskDir = "/baidupcs-server-selftest"
	selfTestReportFile = "selftest_report.log"
	selfTestInterval   = 24 * time.Hour
)

// SelfTestReport 单次自检报告
type SelfTestReport struct {
	Time    time.Time
	OK      bool
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

// RunSelfTest 执行一次完整自检，返回报告
func RunSelfTest() *SelfTestReport {
	report := &SelfTestReport{Time: time.Now(), OK: true}
	report.log("开始自检 " + report.Time.Format("2006-01-02 15:04:05"))

	// 前置检查：必须已登录且有 STOKEN，否则转存必定失败
	user := pcsconfig.Config.ActiveUser()
	if user == nil || user.BDUSS == "" {
		report.fail("未登录百度账号，请先运行 baidupcs-server login")
		writeReport(report)
		return report
	}
	if user.STOKEN == "" && !strings.Contains(user.COOKIES, "STOKEN=") {
		report.fail("STOKEN 未存储。分享转存需要 STOKEN，请重新完整登录：baidupcs-server login")
		writeReport(report)
		return report
	}

	p := pcsconfig.Config.ActiveUserBaiduPCS()

	// ── 步骤1：转存分享链接 ──────────────────────────────────────────
	report.log(fmt.Sprintf("步骤1: 转存分享链接 %s?pwd=%s", selfTestLink, selfTestPwd))

	featureStr := "1pEwltT7wM_275bEhfFmaIQ"

	// 确保测试目录存在
	p.Mkdir(selfTestNetdiskDir)

	transferMu.Lock()
	tokens := p.AccessSharePage(featureStr, true)
	if tokens["ErrMsg"] != "0" {
		transferMu.Unlock()
		report.fail("访问分享页失败: " + tokens["ErrMsg"])
		writeReport(report)
		return report
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
		writeReport(report)
		return report
	}

	p.UpdatePCSCookies(true)
	tokens = p.AccessSharePage(featureStr, false)
	if tokens["ErrMsg"] != "0" {
		transferMu.Unlock()
		report.fail("二次访问分享页失败: " + tokens["ErrMsg"])
		writeReport(report)
		return report
	}

	featureMap := map[string]string{
		"bdstoken": tokens["bdstoken"],
		"root":     "1",
		"web":      "5",
		"app_id":   baidupcs.PanAppID,
		"shorturl": featureStr[1:],
		"channel":  "chunlei",
	}
	queryURL := p.GenerateShareQueryURL("list", featureMap).String()
	transMetas := p.ExtractShareInfo(queryURL, tokens["shareid"], tokens["share_uk"], tokens["bdstoken"])
	if transMetas["ErrMsg"] != "success" {
		transferMu.Unlock()
		report.fail("获取文件列表失败: " + transMetas["ErrMsg"])
		writeReport(report)
		return report
	}

	transMetas["path"] = selfTestNetdiskDir
	transMetas["referer"] = "https://pan.baidu.com/s/" + featureStr
	p.UpdatePCSCookies(true)
	transferResp := p.GenerateRequestQuery("POST", transMetas)
	transferMu.Unlock()

	if transferResp["ErrNo"] != "0" {
		// errno=9 表示文件已存在，视为成功（重复测试时正常）
		if transferResp["ErrNo"] != "9" {
			report.fail("转存失败: " + transferResp["ErrMsg"])
			writeReport(report)
			return report
		}
		report.log("文件已存在，跳过转存（重复测试）")
	} else {
		report.log("✓ 转存成功: " + transferResp["filename"])
	}

	// ── 步骤2：列出测试目录，找到文件 ─────────────────────────────────
	report.log("步骤2: 列出网盘测试目录")
	files, pcsErr := p.FilesDirectoriesList(selfTestNetdiskDir, baidupcs.DefaultOrderOptions)
	if pcsErr != nil {
		report.fail("列目录失败: " + pcsErr.Error())
		writeReport(report)
		return report
	}
	if len(files) == 0 {
		report.fail("测试目录为空")
		writeReport(report)
		return report
	}

	// 找第一个文件（非目录）
	var testFile *baidupcs.FileDirectory
	for _, f := range files {
		if !f.Isdir {
			testFile = f
			break
		}
	}
	if testFile == nil {
		report.fail("测试目录中没有文件")
		writeReport(report)
		return report
	}
	report.log(fmt.Sprintf("✓ 找到测试文件: %s (%.1f KB)",
		testFile.Filename, float64(testFile.Size)/1024))

	// ── 步骤3：获取下载直链 ───────────────────────────────────────────
	report.log("步骤3: 获取下载直链")
	urlInfo, pcsErr2 := p.LocateDownload(testFile.Path)
	if pcsErr2 != nil || urlInfo == nil || len(urlInfo.URLs) == 0 {
		report.fail("获取下载链接失败")
		writeReport(report)
		return report
	}

	// 取第一个非加密链接
	var downloadURL string
	for _, u := range urlInfo.URLs {
		if u.Encrypt == 0 {
			downloadURL = u.URL
			break
		}
	}
	if downloadURL == "" {
		report.fail("无可用下载链接")
		writeReport(report)
		return report
	}
	report.log("✓ 下载链接获取成功")

	// ── 步骤4：下载文件到临时目录 ─────────────────────────────────────
	report.log("步骤4: 下载文件")
	tmpDir, err := os.MkdirTemp("", "baidupcs-selftest-*")
	if err != nil {
		report.fail("创建临时目录失败: " + err.Error())
		writeReport(report)
		return report
	}
	defer os.RemoveAll(tmpDir)

	localFile := filepath.Join(tmpDir, filepath.Base(testFile.Filename))
	if err := downloadToFile(downloadURL, localFile); err != nil {
		report.fail("下载失败: " + err.Error())
		writeReport(report)
		return report
	}

	info, err := os.Stat(localFile)
	if err != nil || info.Size() == 0 {
		report.fail("下载文件为空或不存在")
		writeReport(report)
		return report
	}
	report.log(fmt.Sprintf("✓ 下载成功: %s (%.1f KB)", localFile, float64(info.Size())/1024))

	// ── 步骤5：清理本地临时文件（tmpDir defer 已覆盖）───────────────────
	report.log("步骤5: 清理本地临时文件 ✓")

	// ── 步骤6：清理网盘测试目录 ───────────────────────────────────────
	report.log("步骤6: 清理网盘测试文件")
	if rmErr := p.Remove(testFile.Path); rmErr != nil {
		// 清理失败不影响整体结果，只记录警告
		report.log("⚠ 清理网盘文件失败（不影响结果）: " + rmErr.Error())
	} else {
		report.log("✓ 网盘测试文件已删除")
	}

	// 如果目录空了，顺便删掉
	remaining, _ := p.FilesDirectoriesList(selfTestNetdiskDir, baidupcs.DefaultOrderOptions)
	if len(remaining) == 0 {
		p.Remove(selfTestNetdiskDir)
	}

	report.log("自检完成 ✅ 功能正常")
	writeReport(report)
	return report
}

// downloadToFile 用 HTTP 下载文件，添加百度所需请求头
func downloadToFile(rawURL, destPath string) error {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", baidupcs.NetdiskUA)
	req.Header.Set("Referer", "https://pan.baidu.com/")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// writeReport 将报告追加写入日志文件
func writeReport(r *SelfTestReport) {
	lines := []string{
		strings.Repeat("─", 60),
		fmt.Sprintf("时间: %s", r.Time.Format("2006-01-02 15:04:05")),
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
		fmt.Printf("[自检] 警告: 无法写入报告文件 %s: %s\n", reportPath, err)
		return
	}
	defer f.Close()
	f.WriteString(content)
	fmt.Printf("[自检] 报告已写入: %s\n", reportPath)
}

// StartSelfTestLoop 启动自检循环（服务器启动后5秒执行首次，之后每24小时一次）
func StartSelfTestLoop() {
	go func() {
		fmt.Println("[自检] 将在 5 秒后执行首次自检（使用 --no-test 禁用）")
		time.Sleep(5 * time.Second)

		RunSelfTest()

		ticker := time.NewTicker(selfTestInterval)
		defer ticker.Stop()
		for t := range ticker.C {
			fmt.Printf("[自检] 定时自检触发 %s\n", t.Format("2006-01-02 15:04:05"))
			RunSelfTest()
		}
	}()
}
