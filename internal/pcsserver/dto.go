package pcsserver

import (
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
)

// FileDTO 文件/目录的 API 响应体（去除内部字段，统一 snake_case）
type FileDTO struct {
	FsID     int64  `json:"fs_id"`
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Ctime    int64  `json:"ctime"`
	Mtime    int64  `json:"mtime"`
	MD5      string `json:"md5,omitempty"`
}

func toFileDTO(f *baidupcs.FileDirectory) *FileDTO {
	if f == nil {
		return nil
	}
	return &FileDTO{
		FsID:     f.FsID,
		Path:     f.Path,
		Filename: f.Filename,
		Size:     f.Size,
		IsDir:    f.Isdir,
		Ctime:    f.Ctime,
		Mtime:    f.Mtime,
		MD5:      f.MD5,
	}
}

func toFileDTOList(list baidupcs.FileDirectoryList) []*FileDTO {
	out := make([]*FileDTO, 0, len(list))
	for _, f := range list {
		out = append(out, toFileDTO(f))
	}
	return out
}

// ShareRecordDTO 分享记录 API 响应体
type ShareRecordDTO struct {
	ShareID   int64  `json:"share_id"`
	Link      string `json:"link"`
	Passwd    string `json:"passwd"`
	Path      string `json:"path"`
	ViewCount int    `json:"view_count"`
	ExpireAt  string `json:"expire_at"`
}

func toShareRecordDTO(rec *baidupcs.ShareRecordInfo, passwd string) *ShareRecordDTO {
	expireAt := expireAtStr(rec)
	return &ShareRecordDTO{
		ShareID:   rec.ShareID,
		Link:      rec.Shortlink,
		Passwd:    passwd,
		Path:      rec.TypicalPath,
		ViewCount: rec.ViewCount,
		ExpireAt:  expireAt,
	}
}

func expireAtStr(rec *baidupcs.ShareRecordInfo) string {
	if rec.ExpireType == -1 {
		return "已过期"
	}
	if rec.ExpireTime == 0 {
		return "永久"
	}
	return time.Unix(time.Now().Unix()+rec.ExpireTime, 0).Format("2006-01-02 15:04:05")
}

// LocateDTO 下载直链响应
type LocateDTO struct {
	URLs []string `json:"urls"`
}

func toLocateDTO(info *baidupcs.URLInfo) *LocateDTO {
	if info == nil {
		return &LocateDTO{URLs: []string{}}
	}
	urls := make([]string, 0, len(info.URLs))
	for _, u := range info.URLs {
		if u.Encrypt == 0 {
			urls = append(urls, u.URL)
		}
	}
	return &LocateDTO{URLs: urls}
}

// PanLocateDTO 网盘首页接口直链响应
type PanLocateDTO struct {
	FsID  string `json:"fs_id"`
	Dlink string `json:"dlink"`
}

// TaskDTO 异步任务响应
type TaskDTO struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func toTaskDTO(t *Task) *TaskDTO {
	return &TaskDTO{
		ID:      t.ID,
		Type:    t.Type,
		Status:  string(t.Status),
		Message: t.Message,
	}
}
