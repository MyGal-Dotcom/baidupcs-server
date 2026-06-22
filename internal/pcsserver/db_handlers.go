package pcsserver

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

// dbRequired 检查数据库是否已初始化，未初始化时写 503 并返回 false
func dbRequired(w http.ResponseWriter) bool {
	if db == nil {
		fail(w, 503, "数据库未启用，请使用 --db 参数启动服务器以开启此功能")
		return false
	}
	return true
}

// ---- 工具函数 ----

// parseFeatureStr 从完整的分享 URL 中提取 featureStr（以 '1' 开头的短串）
func parseFeatureStr(link string) (featureStr string, err error) {
	parsedURL, err := url.Parse(link)
	if err != nil {
		return "", fmt.Errorf("链接解析失败: %w", err)
	}
	fs := path.Base(strings.TrimSuffix(parsedURL.Path, "/"))
	if fs == "init" {
		fs = "1" + parsedURL.Query().Get("surl")
	}
	if len(fs) < 2 || len(fs) > 23 || fs[0:1] != "1" {
		return "", fmt.Errorf("链接格式无效")
	}
	return fs, nil
}

// checkShareValid 检测单个 featureStr 是否有效，返回 ShareStatus
func checkShareValid(featureStr string) ShareStatus {
	p := pcsconfig.Config.ActiveUserBaiduPCS()
	tokens := p.AccessSharePage(featureStr, true)
	switch tokens["ErrMsg"] {
	case "0":
		return ShareStatusValid
	case "分享链接已失效", "页面不存在":
		return ShareStatusExpired
	default:
		return ShareStatusUnknown
	}
}

// ---- 保存分享 ----

// handleDBSaveShare 保存分享链接及其文件列表到 DB
// POST /api/db/share/save
// body: {"link":"...","pwd":"..."}
func handleDBSaveShare(w http.ResponseWriter, r *http.Request) {
	if !dbRequired(w) {
		return
	}
	var req struct {
		Link string `json:"link"`
		Pwd  string `json:"pwd"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Link == "" {
		fail(w, 400, "link is required")
		return
	}

	featureStr, err := parseFeatureStr(req.Link)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if len(req.Pwd) != 0 && len(req.Pwd) != 4 {
		fail(w, 400, "pwd must be 4 characters")
		return
	}

	id := LinkID(featureStr)

	// 访问分享页面拿到 shareid / share_uk / bdstoken
	p := pcsconfig.Config.ActiveUserBaiduPCS()
	transferMu.Lock()
	defer transferMu.Unlock()

	tokens := p.AccessSharePage(featureStr, true)
	if tokens["ErrMsg"] == "分享链接已失效" || tokens["ErrMsg"] == "页面不存在" {
		// 仍然记录，标记为已失效
		rec := &ShareRecord{
			ID:         id,
			Link:       req.Link,
			Pwd:        req.Pwd,
			FeatureStr: featureStr,
			Status:     ShareStatusExpired,
		}
		if dbErr := DBSaveShare(rec, nil); dbErr != nil {
			fail(w, 500, "db error: "+dbErr.Error())
			return
		}
		ok(w, map[string]interface{}{"id": id, "status": "expired"})
		return
	}
	if tokens["ErrMsg"] != "0" {
		fail(w, 500, tokens["ErrMsg"])
		return
	}

	// 若有提取码则验证
	if req.Pwd != "" {
		verifyURL := p.GenerateShareQueryURL("verify", map[string]string{
			"shareid":    tokens["shareid"],
			"time":       strconv.FormatInt(time.Now().UnixMilli(), 10),
			"clienttype": "1",
			"uk":         tokens["share_uk"],
		}).String()
		res := p.PostShareQuery(verifyURL, req.Link, map[string]string{
			"pwd":       req.Pwd,
			"vcode":     "null",
			"vcode_str": "null",
			"bdstoken":  tokens["bdstoken"],
		})
		if res["ErrMsg"] != "0" {
			fail(w, 500, "密码验证失败: "+res["ErrMsg"])
			return
		}
		p.UpdatePCSCookies(true)
		tokens = p.AccessSharePage(featureStr, false)
		if tokens["ErrMsg"] != "0" {
			fail(w, 500, tokens["ErrMsg"])
			return
		}
	}

	// 拉取文件列表
	featureMap := map[string]string{
		"bdstoken": tokens["bdstoken"],
		"root":     "1",
		"web":      "5",
		"app_id":   baidupcs.PanAppID,
		"shorturl": featureStr[1:],
		"channel":  "chunlei",
	}
	queryURL := p.GenerateShareQueryURL("list", featureMap).String()
	meta := p.ExtractShareInfo(queryURL, tokens["shareid"], tokens["share_uk"], tokens["bdstoken"])
	if meta["ErrMsg"] != "success" {
		fail(w, 500, meta["ErrMsg"])
		return
	}

	fileCount, _ := strconv.Atoi(meta["item_num"])
	rec := &ShareRecord{
		ID:         id,
		Link:       req.Link,
		Pwd:        req.Pwd,
		FeatureStr: featureStr,
		Title:      meta["filename"],
		FileCount:  fileCount,
		Status:     ShareStatusValid,
	}

	// ExtractShareInfo 返回的是 meta["filename"]（主文件名）
	// 把文件名保存到文件列表中
	shareFiles := make([]*ShareFile, 0)
	if name := meta["filename"]; name != "" {
		shareFiles = append(shareFiles, &ShareFile{
			ShareID:  id,
			Filename: name,
		})
	}

	if dbErr := DBSaveShare(rec, shareFiles); dbErr != nil {
		fail(w, 500, "db error: "+dbErr.Error())
		return
	}

	ok(w, map[string]interface{}{
		"id":         id,
		"title":      rec.Title,
		"file_count": fileCount,
		"status":     "valid",
	})
}

// ---- 列出 / 获取 ----

// handleDBListShares GET /api/db/shares
func handleDBListShares(w http.ResponseWriter, r *http.Request) {
	if !dbRequired(w) {
		return
	}
	records, err := DBListShares()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	ok(w, records)
}

// handleDBGetShare GET /api/db/share?id=
func handleDBGetShare(w http.ResponseWriter, r *http.Request) {
	if !dbRequired(w) {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		fail(w, 400, "id is required")
		return
	}
	detail, err := DBGetShare(id)
	if err != nil {
		fail(w, 404, "share not found: "+err.Error())
		return
	}
	ok(w, detail)
}

// ---- 有效性检测 ----

type checkResult struct {
	ID     string      `json:"id"`
	Status ShareStatus `json:"status"`
	Msg    string      `json:"msg"`
}

// handleDBCheckShare POST /api/db/share/check
// body: {"id":"..."} 或 {"link":"..."}  检测单条
func handleDBCheckShare(w http.ResponseWriter, r *http.Request) {
	if !dbRequired(w) {
		return
	}
	var req struct {
		ID   string `json:"id"`
		Link string `json:"link"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	var id, featureStr string
	if req.ID != "" {
		detail, err := DBGetShare(req.ID)
		if err != nil {
			fail(w, 404, "share not found")
			return
		}
		id = detail.ID
		featureStr = detail.FeatureStr
	} else if req.Link != "" {
		var err error
		featureStr, err = parseFeatureStr(req.Link)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		id = LinkID(featureStr)
	} else {
		fail(w, 400, "id or link is required")
		return
	}

	status := checkShareValid(featureStr)
	if id != "" {
		_ = DBUpdateStatus(id, status)
	}

	msg := map[ShareStatus]string{
		ShareStatusValid:   "有效",
		ShareStatusExpired: "已失效",
		ShareStatusUnknown: "未知",
	}[status]

	ok(w, checkResult{ID: id, Status: status, Msg: msg})
}

// handleDBCheckAll POST /api/db/share/check-all  批量检测所有保存的分享
func handleDBCheckAll(w http.ResponseWriter, r *http.Request) {
	if !dbRequired(w) {
		return
	}
	ids, err := DBGetAllShareIDs()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}

	task := newTask("check-all")
	go func() {
		task.setRunning()

		var valid, expired, unknown int32
		var wg sync.WaitGroup
		// 限制并发数避免触发风控
		sem := make(chan struct{}, 3)

		for _, id := range ids {
			wg.Add(1)
			sem <- struct{}{}
			go func(shareID string) {
				defer wg.Done()
				defer func() { <-sem }()

				detail, dbErr := DBGetShare(shareID)
				if dbErr != nil {
					return
				}
				status := checkShareValid(detail.FeatureStr)
				_ = DBUpdateStatus(shareID, status)
				switch status {
				case ShareStatusValid:
					atomic.AddInt32(&valid, 1)
				case ShareStatusExpired:
					atomic.AddInt32(&expired, 1)
				default:
					atomic.AddInt32(&unknown, 1)
				}
			}(id)
		}
		wg.Wait()
		task.setDone(fmt.Sprintf("total=%d valid=%d expired=%d unknown=%d",
			len(ids), valid, expired, unknown))
	}()

	ok(w, map[string]interface{}{
		"task_id": task.ID,
		"total":   len(ids),
	})
}

// ---- 补链 ----

// handleDBRelink POST /api/db/share/relink
// 尝试为失效分享在用户网盘中找到对应文件并重新分享
// body: {"id":"...","save_path":"/my/dir/"}
func handleDBRelink(w http.ResponseWriter, r *http.Request) {
	if !dbRequired(w) {
		return
	}
	var req struct {
		ID       string `json:"id"`
		SavePath string `json:"save_path"` // 在哪个目录下搜索，默认 /
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ID == "" {
		fail(w, 400, "id is required")
		return
	}

	detail, err := DBGetShare(req.ID)
	if err != nil {
		fail(w, 404, "share not found")
		return
	}
	if len(detail.Files) == 0 {
		fail(w, 400, "no file records saved for this share, cannot relink")
		return
	}

	searchRoot := req.SavePath
	if searchRoot == "" {
		searchRoot = pcsconfig.Config.ActiveUser().Workdir
	}

	p := pcsconfig.Config.ActiveUserBaiduPCS()

	// 用第一个文件的名字在网盘中搜索
	keyword := detail.Files[0].Filename
	files, searchErr := p.Search(searchRoot, keyword, true)
	if searchErr != nil {
		fail(w, 500, "search error: "+searchErr.Error())
		return
	}
	if len(files) == 0 {
		fail(w, 404, fmt.Sprintf("在 %s 中未找到文件 '%s'，请先转存或上传", searchRoot, keyword))
		return
	}

	// 找到匹配文件，尝试重新分享
	var targets []string
	for _, f := range files {
		targets = append(targets, f.Path)
		if len(targets) >= 20 { // 百度单次分享上限
			break
		}
	}

	opt := &baidupcs.ShareOption{
		Password:   "",
		Period:     0,
		IsCombined: true,
	}
	shared, shareErr := p.ShareSet(targets, opt)
	if shareErr != nil {
		fail(w, 500, "重新分享失败: "+shareErr.Error())
		return
	}

	newLink := shared.Link + "?pwd=" + shared.Pwd

	// 更新 DB：将旧记录更新为新链接
	newFeatureStr, _ := parseFeatureStr(shared.Link)
	if updateErr := DBUpdateLink(req.ID, newLink, shared.Pwd, newFeatureStr); updateErr != nil {
		// 即使更新失败也返回新链接
		fail(w, 500, fmt.Sprintf("分享成功但更新DB失败: %s, 新链接: %s", updateErr, newLink))
		return
	}

	ok(w, map[string]interface{}{
		"new_id":          LinkID(newFeatureStr),
		"new_link":        newLink,
		"pwd":             shared.Pwd,
		"matched_files":   len(targets),
		"keyword_used":    keyword,
	})
}

// ---- 删除 ----

// handleDBDeleteShare DELETE /api/db/share?id=
func handleDBDeleteShare(w http.ResponseWriter, r *http.Request) {
	if !dbRequired(w) {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		fail(w, 400, "id is required")
		return
	}
	if err := DBDeleteShare(id); err != nil {
		fail(w, 500, err.Error())
		return
	}
	ok(w, nil)
}

// ---- 文件搜索 ----

// handleDBSearchFiles GET /api/db/files/search?keyword=
func handleDBSearchFiles(w http.ResponseWriter, r *http.Request) {
	if !dbRequired(w) {
		return
	}
	keyword := r.URL.Query().Get("keyword")
	if keyword == "" {
		fail(w, 400, "keyword is required")
		return
	}
	files, err := DBSearchFiles(keyword)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	ok(w, files)
}
