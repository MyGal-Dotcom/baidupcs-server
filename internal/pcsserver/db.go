package pcsserver

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var db *sql.DB

const schema = `
CREATE TABLE IF NOT EXISTS share_links (
    id          TEXT PRIMARY KEY,
    link        TEXT NOT NULL,
    pwd         TEXT NOT NULL DEFAULT '',
    feature_str TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL DEFAULT '',
    file_count  INTEGER NOT NULL DEFAULT 0,
    status      INTEGER NOT NULL DEFAULT 0,
    saved_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    checked_at  DATETIME
);

CREATE TABLE IF NOT EXISTS share_files (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    share_id   TEXT NOT NULL,
    filename   TEXT NOT NULL,
    path       TEXT NOT NULL DEFAULT '',
    fs_id      INTEGER NOT NULL DEFAULT 0,
    size       INTEGER NOT NULL DEFAULT 0,
    is_dir     INTEGER NOT NULL DEFAULT 0,
    md5        TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (share_id) REFERENCES share_links(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_share_files_share_id ON share_files(share_id);
CREATE INDEX IF NOT EXISTS idx_share_files_filename  ON share_files(filename);
`

// ShareStatus 分享链接状态
type ShareStatus int

const (
	ShareStatusUnknown ShareStatus = 0
	ShareStatusValid   ShareStatus = 1
	ShareStatusExpired ShareStatus = 2
)

// ShareRecord DB 中的分享记录
type ShareRecord struct {
	ID         string      `json:"id"`
	Link       string      `json:"link"`
	Pwd        string      `json:"pwd"`
	FeatureStr string      `json:"feature_str"`
	Title      string      `json:"title"`
	FileCount  int         `json:"file_count"`
	Status     ShareStatus `json:"status"`
	SavedAt    time.Time   `json:"saved_at"`
	CheckedAt  *time.Time  `json:"checked_at,omitempty"`
}

// ShareFile DB 中的分享内文件记录
type ShareFile struct {
	ID       int64  `json:"id"`
	ShareID  string `json:"share_id"`
	Filename string `json:"filename"`
	Path     string `json:"path"`
	FsID     int64  `json:"fs_id"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	MD5      string `json:"md5"`
}

// ShareDetail 分享详情（含文件列表）
type ShareDetail struct {
	ShareRecord
	Files []*ShareFile `json:"files"`
}

// LinkID 根据 featureStr 计算主键：SHA256 前 16 字节转十六进制
func LinkID(featureStr string) string {
	h := sha256.Sum256([]byte(strings.ToLower(featureStr)))
	return hex.EncodeToString(h[:8])
}

// InitDB 初始化数据库
func InitDB(path string) error {
	var err error
	db, err = sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite 单写者
	_, err = db.Exec(schema)
	return err
}

// DBSaveShare 保存分享记录（及文件列表）
func DBSaveShare(rec *ShareRecord, files []*ShareFile) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO share_links (id, link, pwd, feature_str, title, file_count, status, saved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			link=excluded.link, pwd=excluded.pwd, title=excluded.title,
			file_count=excluded.file_count, status=excluded.status`,
		rec.ID, rec.Link, rec.Pwd, rec.FeatureStr, rec.Title,
		rec.FileCount, int(rec.Status), time.Now())
	if err != nil {
		return err
	}

	// 更新文件列表：先清旧的
	_, err = tx.Exec(`DELETE FROM share_files WHERE share_id = ?`, rec.ID)
	if err != nil {
		return err
	}
	for _, f := range files {
		isDir := 0
		if f.IsDir {
			isDir = 1
		}
		_, err = tx.Exec(`
			INSERT INTO share_files (share_id, filename, path, fs_id, size, is_dir, md5)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			rec.ID, f.Filename, f.Path, f.FsID, f.Size, isDir, f.MD5)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DBListShares 列出所有分享记录
func DBListShares() ([]*ShareRecord, error) {
	rows, err := db.Query(`
		SELECT id, link, pwd, feature_str, title, file_count, status, saved_at, checked_at
		FROM share_links ORDER BY saved_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records, err := scanShareRows(rows)
	if records == nil {
		records = []*ShareRecord{}
	}
	return records, err
}

// DBGetShare 获取分享详情（含文件列表）
func DBGetShare(id string) (*ShareDetail, error) {
	row := db.QueryRow(`
		SELECT id, link, pwd, feature_str, title, file_count, status, saved_at, checked_at
		FROM share_links WHERE id = ?`, id)
	rec, err := scanShareRow(row)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(`
		SELECT id, share_id, filename, path, fs_id, size, is_dir, md5
		FROM share_files WHERE share_id = ? ORDER BY is_dir DESC, filename`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	files := make([]*ShareFile, 0)
	for rows.Next() {
		f := &ShareFile{}
		var isDir int
		if err := rows.Scan(&f.ID, &f.ShareID, &f.Filename, &f.Path, &f.FsID, &f.Size, &isDir, &f.MD5); err != nil {
			return nil, err
		}
		f.IsDir = isDir == 1
		files = append(files, f)
	}
	return &ShareDetail{ShareRecord: *rec, Files: files}, nil
}

// DBUpdateStatus 更新分享状态
func DBUpdateStatus(id string, status ShareStatus) error {
	now := time.Now()
	_, err := db.Exec(`UPDATE share_links SET status=?, checked_at=? WHERE id=?`, int(status), now, id)
	return err
}

// DBUpdateLink 更新分享链接（用于补链）
func DBUpdateLink(id, newLink, newPwd, newFeatureStr string) error {
	newID := LinkID(newFeatureStr)
	_, err := db.Exec(`
		UPDATE share_links SET link=?, pwd=?, feature_str=?, id=?, status=?
		WHERE id=?`,
		newLink, newPwd, newFeatureStr, newID, int(ShareStatusValid), id)
	return err
}

// DBDeleteShare 删除分享记录
func DBDeleteShare(id string) error {
	_, err := db.Exec(`DELETE FROM share_links WHERE id=?`, id)
	return err
}

// DBSearchFiles 在已保存的文件中搜索
func DBSearchFiles(keyword string) ([]*ShareFile, error) {
	rows, err := db.Query(`
		SELECT id, share_id, filename, path, fs_id, size, is_dir, md5
		FROM share_files WHERE filename LIKE ? ORDER BY filename`,
		"%"+keyword+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	files := make([]*ShareFile, 0)
	for rows.Next() {
		f := &ShareFile{}
		var isDir int
		if err := rows.Scan(&f.ID, &f.ShareID, &f.Filename, &f.Path, &f.FsID, &f.Size, &isDir, &f.MD5); err != nil {
			return nil, err
		}
		f.IsDir = isDir == 1
		files = append(files, f)
	}
	return files, nil
}

// DBGetAllShareIDs 获取所有分享ID（用于批量检测）
func DBGetAllShareIDs() ([]string, error) {
	rows, err := db.Query(`SELECT id FROM share_links`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// DBGetShareByFeature 通过 featureStr 查询
func DBGetShareByFeature(featureStr string) (*ShareRecord, error) {
	row := db.QueryRow(`
		SELECT id, link, pwd, feature_str, title, file_count, status, saved_at, checked_at
		FROM share_links WHERE feature_str = ?`, featureStr)
	return scanShareRow(row)
}

func scanShareRow(row *sql.Row) (*ShareRecord, error) {
	rec := &ShareRecord{}
	var checkedAt sql.NullTime
	err := row.Scan(&rec.ID, &rec.Link, &rec.Pwd, &rec.FeatureStr,
		&rec.Title, &rec.FileCount, &rec.Status, &rec.SavedAt, &checkedAt)
	if err != nil {
		return nil, err
	}
	if checkedAt.Valid {
		rec.CheckedAt = &checkedAt.Time
	}
	return rec, nil
}

func scanShareRows(rows *sql.Rows) ([]*ShareRecord, error) {
	var records []*ShareRecord
	for rows.Next() {
		rec := &ShareRecord{}
		var checkedAt sql.NullTime
		if err := rows.Scan(&rec.ID, &rec.Link, &rec.Pwd, &rec.FeatureStr,
			&rec.Title, &rec.FileCount, &rec.Status, &rec.SavedAt, &checkedAt); err != nil {
			return nil, err
		}
		if checkedAt.Valid {
			rec.CheckedAt = &checkedAt.Time
		}
		records = append(records, rec)
	}
	return records, nil
}
