package pcsserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcscommand"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

// transferMu 保护 transfer 操作中的 cookie 修改，避免并发冲突
var transferMu sync.Mutex

func pcs() *baidupcs.BaiduPCS {
	return pcsconfig.Config.ActiveUserBaiduPCS()
}

func activeUser() *pcsconfig.Baidu {
	return pcsconfig.Config.ActiveUser()
}

func joinPath(p string) string {
	return activeUser().PathJoin(p)
}

// decodeJSON 解析请求体，失败时写错误响应并返回 false
func decodeJSON(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		fail(w, 400, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

// ---- 用户 / 配额 ----

func handleUser(w http.ResponseWriter, r *http.Request) {
	u := activeUser()
	ok(w, map[string]interface{}{
		"uid":     u.UID,
		"name":    u.Name,
		"workdir": u.Workdir,
	})
}

func handleQuota(w http.ResponseWriter, r *http.Request) {
	quota, used, pcsErr := pcs().QuotaInfo()
	if pcsErr != nil {
		fail(w, 500, sanitizeErr(pcsErr.Error()))
		return
	}
	ok(w, map[string]int64{"total": quota, "used": used, "free": quota - used})
}

// ---- 文件列表 / 元信息 / 搜索 ----

func handleLs(w http.ResponseWriter, r *http.Request) {
	pcspath := joinPath(r.URL.Query().Get("path"))
	if pcspath == "" {
		pcspath = "/"
	}
	files, err := pcs().FilesDirectoriesList(pcspath, baidupcs.DefaultOrderOptions)
	if err != nil {
		fail(w, 500, sanitizeErr(err.Error()))
		return
	}
	ok(w, toFileDTOList(files))
}

func handleMeta(w http.ResponseWriter, r *http.Request) {
	rawPaths := r.URL.Query().Get("paths")
	if rawPaths == "" {
		fail(w, 400, "paths is required")
		return
	}
	parts := strings.Split(rawPaths, ",")
	pcsPaths := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			pcsPaths = append(pcsPaths, joinPath(p))
		}
	}
	files, pcsErr := pcs().FilesDirectoriesBatchMeta(pcsPaths...)
	if pcsErr != nil {
		fail(w, 500, sanitizeErr(pcsErr.Error()))
		return
	}
	ok(w, toFileDTOList(files))
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pcspath := joinPath(q.Get("path"))
	keyword := q.Get("keyword")
	recurse := q.Get("recurse") == "1" || q.Get("recurse") == "true"
	if keyword == "" {
		fail(w, 400, "keyword is required")
		return
	}
	files, err := pcs().Search(pcspath, keyword, recurse)
	if err != nil {
		fail(w, 500, sanitizeErr(err.Error()))
		return
	}
	ok(w, toFileDTOList(files))
}

// ---- 目录操作 ----

func handleMkdir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Path == "" {
		fail(w, 400, "path is required")
		return
	}
	if err := pcs().Mkdir(joinPath(req.Path)); err != nil {
		fail(w, 500, sanitizeErr(err.Error()))
		return
	}
	ok(w, nil)
}

// ---- 删除 ----

func handleRm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Paths) == 0 {
		fail(w, 400, "paths is required")
		return
	}
	pcsPaths := make([]string, len(req.Paths))
	for i, p := range req.Paths {
		pcsPaths[i] = joinPath(p)
	}
	if err := pcs().Remove(pcsPaths...); err != nil {
		fail(w, 500, sanitizeErr(err.Error()))
		return
	}
	ok(w, nil)
}

// ---- 重命名 ----

func handleRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.From == "" || req.To == "" {
		fail(w, 400, "from and to are required")
		return
	}
	if err := pcs().Rename(joinPath(req.From), joinPath(req.To)); err != nil {
		fail(w, 500, sanitizeErr(err.Error()))
		return
	}
	ok(w, nil)
}

// ---- 移动 ----

func handleMv(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FromTo []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"from_to"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.FromTo) == 0 {
		fail(w, 400, "from_to is required")
		return
	}
	list := make([]*baidupcs.CpMvJSON, len(req.FromTo))
	for i, ft := range req.FromTo {
		list[i] = &baidupcs.CpMvJSON{From: joinPath(ft.From), To: joinPath(ft.To)}
	}
	if err := pcs().Move(list...); err != nil {
		fail(w, 500, sanitizeErr(err.Error()))
		return
	}
	ok(w, nil)
}

// ---- 复制 ----

func handleCp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FromTo []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"from_to"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.FromTo) == 0 {
		fail(w, 400, "from_to is required")
		return
	}
	list := make([]*baidupcs.CpMvJSON, len(req.FromTo))
	for i, ft := range req.FromTo {
		list[i] = &baidupcs.CpMvJSON{From: joinPath(ft.From), To: joinPath(ft.To)}
	}
	if err := pcs().Copy(list...); err != nil {
		fail(w, 500, sanitizeErr(err.Error()))
		return
	}
	ok(w, nil)
}

// ---- 分享链接转存 ----

func handleTransfer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Link    string `json:"link"`
		Pwd     string `json:"pwd"`
		SaveTo  string `json:"saveto"`  // 保存目标目录，默认当前工作目录
		Collect bool   `json:"collect"` // 多文件合并到同一子目录
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Link == "" {
		fail(w, 400, "link is required")
		return
	}

	parsedURL, err := url.Parse(req.Link)
	if err != nil {
		fail(w, 400, "invalid link: "+err.Error())
		return
	}

	extraCode := req.Pwd
	if extraCode == "" {
		extraCode = parsedURL.Query().Get("pwd")
	}

	featureStr := path.Base(strings.TrimSuffix(parsedURL.Path, "/"))
	if featureStr == "init" {
		featureStr = "1" + parsedURL.Query().Get("surl")
	}

	if len(featureStr) > 23 || len(featureStr) < 2 || featureStr[0:1] != "1" {
		fail(w, 400, "invalid share link format")
		return
	}
	if len(extraCode) != 4 {
		fail(w, 400, "pwd must be 4 characters")
		return
	}

	transferMu.Lock()
	defer transferMu.Unlock()

	p := pcs()

	tokens := p.AccessSharePage(featureStr, true)
	if tokens["ErrMsg"] != "0" {
		fail(w, 500, tokens["ErrMsg"])
		return
	}

	verifyURL := p.GenerateShareQueryURL("verify", map[string]string{
		"shareid":    tokens["shareid"],
		"time":       strconv.FormatInt(time.Now().UnixMilli(), 10),
		"clienttype": "1",
		"uk":         tokens["share_uk"],
	}).String()

	res := p.PostShareQuery(verifyURL, req.Link, map[string]string{
		"pwd":       extraCode,
		"vcode":     "null",
		"vcode_str": "null",
		"bdstoken":  tokens["bdstoken"],
	})
	if res["ErrMsg"] != "0" {
		fail(w, 500, res["ErrMsg"])
		return
	}

	p.UpdatePCSCookies(true)

	tokens = p.AccessSharePage(featureStr, false)
	if tokens["ErrMsg"] != "0" {
		fail(w, 500, tokens["ErrMsg"])
		return
	}

	featureMap := map[string]string{
		"bdstoken": tokens["bdstoken"],
		"root":     "1",
		"web":      "5",
		"app_id":   baidupcs.PanAppID,
		"shorturl": featureStr[1:],
		"channel":  "chunlei",
	}
	queryShareInfoURL := p.GenerateShareQueryURL("list", featureMap).String()
	transMetas := p.ExtractShareInfo(queryShareInfoURL, tokens["shareid"], tokens["share_uk"], tokens["bdstoken"])
	if transMetas["ErrMsg"] != "success" {
		fail(w, 500, transMetas["ErrMsg"])
		return
	}

	saveTo := req.SaveTo
	if saveTo == "" {
		saveTo = activeUser().Workdir
	} else {
		saveTo = joinPath(saveTo)
	}

	transMetas["path"] = saveTo
	if transMetas["item_num"] != "1" && req.Collect {
		dirName := transMetas["filename"] + "等文件"
		transMetas["path"] = path.Join(saveTo, dirName)
		p.Mkdir(transMetas["path"])
	}
	transMetas["referer"] = "https://pan.baidu.com/s/" + featureStr

	p.UpdatePCSCookies(true)
	resp := p.GenerateRequestQuery("POST", transMetas)
	if resp["ErrNo"] != "0" {
		fail(w, 500, resp["ErrMsg"])
		return
	}

	filename := resp["filename"]
	if req.Collect {
		filename = transMetas["filename"]
	}
	ok(w, map[string]string{
		"filename": filename,
		"saveto":   transMetas["path"],
	})
}

// ---- 获取下载直链 ----

func handleLocate(w http.ResponseWriter, r *http.Request) {
	pcspath := joinPath(r.URL.Query().Get("path"))
	if pcspath == "" {
		fail(w, 400, "path is required")
		return
	}

	// 先查 meta：确认存在且不是目录
	fileMeta, pcsErr := pcs().FilesDirectoriesMeta(pcspath)
	if pcsErr != nil {
		fail(w, 404, sanitizeErr(pcsErr.Error()))
		return
	}
	if fileMeta.Isdir {
		fail(w, 400, "path is a directory, not a file")
		return
	}

	fromPan := r.URL.Query().Get("from_pan") == "1"
	if fromPan {
		dlinks, pcsErr2 := pcs().LocatePanAPIDownload(fileMeta.FsID)
		if pcsErr2 != nil {
			fail(w, 500, sanitizeErr(pcsErr2.Error()))
			return
		}
		out := make([]*PanLocateDTO, 0, len(dlinks))
		for _, d := range dlinks {
			out = append(out, &PanLocateDTO{FsID: d.FsID, Dlink: d.Dlink})
		}
		ok(w, out)
		return
	}
	dlinks, pcsErr := pcs().LocateDownload(pcspath)
	if pcsErr != nil {
		fail(w, 500, sanitizeErr(pcsErr.Error()))
		return
	}
	ok(w, toLocateDTO(dlinks))
}

// ---- 异步下载到本地 ----

func handleDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths    []string `json:"paths"`
		SaveTo   string   `json:"saveto"`
		Parallel int      `json:"parallel"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Paths) == 0 {
		fail(w, 400, "paths is required")
		return
	}

	pcsPaths := make([]string, len(req.Paths))
	for i, p := range req.Paths {
		pcsPaths[i] = joinPath(p)
	}

	// 沙箱检查：saveto 必须在配置的 SaveDir 内，防止任意路径写入
	safeDir, err := safeSaveTo(req.SaveTo)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}

	task := newTask("download")
	go func() {
		task.setRunning()
		opts := &pcscommand.DownloadOptions{
			SaveTo:   safeDir,
			Parallel: req.Parallel,
		}
		pcscommand.RunDownload(pcsPaths, opts)
		task.setDone(fmt.Sprintf("downloaded %d path(s)", len(pcsPaths)))
	}()

	ok(w, map[string]string{"task_id": task.ID})
}

// ---- 分享管理 ----

func handleShareSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths  []string `json:"paths"`
		Period int      `json:"period"` // 有效天数 0=永久
		Pwd    string   `json:"pwd"`    // 提取码，空则不加密
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Paths) == 0 {
		fail(w, 400, "paths is required")
		return
	}
	pcsPaths := make([]string, len(req.Paths))
	for i, p := range req.Paths {
		pcsPaths[i] = joinPath(p)
	}
	opt := &baidupcs.ShareOption{
		Period:     req.Period,
		Password:   req.Pwd,
		IsCombined: true,
	}
	shared, err := pcs().ShareSet(pcsPaths, opt)
	if err != nil {
		fail(w, 500, sanitizeErr(err.Error()))
		return
	}
	ok(w, map[string]interface{}{
		"share_id": shared.ShareID,
		"link":     shared.Link + "?pwd=" + shared.Pwd,
		"pwd":      shared.Pwd,
	})
}

func handleShareCancel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		fail(w, 400, "ids is required")
		return
	}
	if err := pcs().ShareCancel(req.IDs); err != nil {
		fail(w, 500, sanitizeErr(err.Error()))
		return
	}
	ok(w, nil)
}

func handleShareList(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	records, err := pcs().ShareList(page)
	if err != nil {
		fail(w, 500, sanitizeErr(err.Error()))
		return
	}
	p := pcs()
	out := make([]*ShareRecordDTO, 0, len(records))
	for _, rec := range records {
		var passwd string
		if rec.Public == 0 && rec.ExpireType != -1 {
			info, _ := p.ShareSURLInfo(rec.ShareID)
			if info != nil {
				passwd = strings.TrimSpace(info.Pwd)
			}
		}
		out = append(out, toShareRecordDTO(rec, passwd))
	}
	ok(w, out)
}

// ---- 任务查询 ----

func handleTasks(w http.ResponseWriter, r *http.Request) {
	tasks := listTasks()
	out := make([]*TaskDTO, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, toTaskDTO(t))
	}
	ok(w, out)
}

func handleTask(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		fail(w, 400, "id is required")
		return
	}
	t, found := getTask(id)
	if !found {
		fail(w, 404, "task not found")
		return
	}
	ok(w, toTaskDTO(t))
}

// handleSelfTest POST /api/selftest  手动触发一次自检（异步执行）
func handleSelfTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "method not allowed, use POST")
		return
	}
	task := newTask("selftest")
	go func() {
		task.setRunning()
		report := RunSelfTest()
		if report.OK {
			task.setDone("功能正常")
		} else {
			task.setFailed(report.Message)
		}
	}()
	ok(w, map[string]string{"task_id": task.ID, "msg": "自检已启动，通过 /api/task?id= 查询结果"})
}
