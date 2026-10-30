package server

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"homelab-teach/internal/content"
	"homelab-teach/internal/db"
)

const maxImportBytes = 200 << 20

type importFileError struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type importResult struct {
	Created        int               `json:"created"`
	Updated        int               `json:"updated"`
	Skipped        int               `json:"skipped"`
	Ignored        []string          `json:"ignored"`
	Errors         []importFileError `json:"errors"`
	FoldersCreated []string          `json:"folders_created"`
}

var errBatchTooLarge = errors.New("batch too large")

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "multipart 格式错误")
		return
	}

	ctx := r.Context()
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		s.internal(w, "开启导入事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	res := &importResult{
		Ignored:        []string{},
		Errors:         []importFileError{},
		FoldersCreated: []string{},
	}
	targetID := int64(1)
	targetKnown := false
	filesSeen := false
	targetTooLate := false

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if isMaxBytes(err) {
				writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "整批导入不能超过 200MB")
				return
			}
			writeErr(w, http.StatusBadRequest, "bad_request", "multipart 格式错误")
			return
		}

		switch part.FormName() {
		case "target_folder_id":
			raw, err := io.ReadAll(io.LimitReader(part, 64))
			if err != nil {
				if isMaxBytes(err) {
					writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "整批导入不能超过 200MB")
					return
				}
				writeErr(w, http.StatusBadRequest, "bad_request", "读取 target_folder_id 失败")
				return
			}
			id, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "bad_request", "target_folder_id 必须是整数")
				return
			}
			if _, err := s.store.Folder(ctx, tx, id); errors.Is(err, db.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "folder_not_found", "目标文件夹不存在")
				return
			} else if err != nil {
				s.internal(w, "读取目标文件夹失败", err)
				return
			}
			if filesSeen && !targetKnown && id != 1 {
				targetTooLate = true
			}
			targetID = id
			targetKnown = true

		case "files":
			filesSeen = true
			rawPath := rawPartFilename(part)
			if err := s.importOne(ctx, tx, targetID, rawPath, part, res); err != nil {
				if errors.Is(err, errBatchTooLarge) || isMaxBytes(err) {
					writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "整批导入不能超过 200MB")
					return
				}
				s.internal(w, "导入课件失败", err)
				return
			}
		}
	}

	if targetTooLate {
		writeErr(w, http.StatusBadRequest, "bad_request", "target_folder_id 必须位于文件字段之前")
		return
	}
	if err := tx.Commit(); err != nil {
		s.internal(w, "提交导入事务失败", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) importOne(ctx context.Context, tx *sql.Tx, targetID int64, rawPath string, part *multipart.Part, res *importResult) error {
	if rawPath == "" {
		res.Errors = append(res.Errors, importFileError{Path: rawPath, Reason: "invalid_path"})
		return nil
	}
	if !content.IsHTMLFilename(rawPath) {
		res.Ignored = append(res.Ignored, rawPath)
		return nil
	}
	segs, ok := splitImportPath(rawPath)
	if !ok {
		res.Errors = append(res.Errors, importFileError{Path: rawPath, Reason: "invalid_path"})
		return nil
	}
	filename := segs[len(segs)-1]

	raw, err := io.ReadAll(io.LimitReader(part, maxPageBytes+1))
	if err != nil {
		if isMaxBytes(err) {
			return errBatchTooLarge
		}
		return err
	}
	if len(raw) > maxPageBytes {
		res.Errors = append(res.Errors, importFileError{Path: rawPath, Reason: "too_large"})
		return nil
	}

	prep, code := preparePage(filename, raw)
	if code != "" {
		res.Errors = append(res.Errors, importFileError{Path: rawPath, Reason: code})
		return nil
	}

	folderID, err := s.ensureFolders(ctx, tx, targetID, segs[:len(segs)-1], &res.FoldersCreated)
	if err != nil {
		return err
	}

	sort := int64(0)
	if lesson, ok := content.MetaLesson(raw); ok {
		sort = int64(lesson) * 1000
	}

	status, _, err := s.ingestPage(ctx, tx, folderID, filename, raw, prep, sort)
	if err != nil {
		return err
	}
	switch status {
	case "created":
		res.Created++
	case "updated":
		res.Updated++
	case "skipped":
		res.Skipped++
	}
	return nil
}

func (s *Server) ensureFolders(ctx context.Context, q db.Querier, base int64, segs []string, created *[]string) (int64, error) {
	cur := base
	prefix := ""
	for _, name := range segs {
		if prefix == "" {
			prefix = name
		} else {
			prefix += "/" + name
		}
		var id int64
		err := q.QueryRowContext(ctx, `SELECT id FROM folders WHERE parent_id = ? AND name = ?`, cur, name).Scan(&id)
		if err == nil {
			cur = id
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		err = q.QueryRowContext(ctx,
			`INSERT INTO folders(parent_id, name, is_public, sort) VALUES (?, ?, NULL, 0) RETURNING id`,
			cur, name).Scan(&id)
		if err != nil {
			return 0, err
		}
		*created = append(*created, prefix)
		cur = id
	}
	return cur, nil
}

func splitImportPath(raw string) ([]string, bool) {
	if raw == "" || strings.Contains(raw, "\\") || strings.ContainsRune(raw, 0) {
		return nil, false
	}
	parts := strings.Split(raw, "/")
	if len(parts) == 0 {
		return nil, false
	}
	out := make([]string, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "." || strings.Contains(p, "..") {
			return nil, false
		}
		if i == len(parts)-1 {
			if !validFilename(p) {
				return nil, false
			}
		} else if utf8.RuneCountInString(p) > 64 {
			return nil, false
		}
		out[i] = p
	}
	return out, true
}

func rawPartFilename(p *multipart.Part) string {
	_, params, err := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
	if err != nil {
		return ""
	}
	return params["filename"]
}

func isMaxBytes(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}
