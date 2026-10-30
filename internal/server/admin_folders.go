package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"homelab-teach/internal/db"
)

type adminFolder struct {
	ID        int64  `json:"id"`
	ParentID  int64  `json:"parent_id"`
	Name      string `json:"name"`
	IsPublic  any    `json:"is_public"`
	Sort      int64  `json:"sort"`
	PageCount int    `json:"page_count"`
}

type folderResponse struct {
	ID       int64  `json:"id"`
	ParentID int64  `json:"parent_id"`
	Name     string `json:"name"`
	IsPublic any    `json:"is_public"`
	Sort     int64  `json:"sort"`
}

func publicValue(p *int) any {
	if p == nil {
		return "inherit"
	}
	return *p
}

func (s *Server) handleAdminTree(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	folders, err := s.store.Folders(ctx, s.store.DB)
	if err != nil {
		s.internal(w, "读取文件夹失败", err)
		return
	}
	counts, err := s.store.PageCounts(ctx, s.store.DB)
	if err != nil {
		s.internal(w, "统计课件数失败", err)
		return
	}

	out := make([]adminFolder, 0, len(folders))
	for _, f := range folders {
		if f.ID == 1 {
			continue
		}
		out = append(out, adminFolder{
			ID:        f.ID,
			ParentID:  f.ParentID,
			Name:      f.Name,
			IsPublic:  publicValue(f.IsPublic),
			Sort:      f.Sort,
			PageCount: counts[f.ID],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": 1, "folders": out})
}

func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ParentID int64  `json:"parent_id"`
		Name     string `json:"name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "JSON 格式错误")
		return
	}
	if req.ParentID == 0 {
		writeErr(w, http.StatusBadRequest, "bad_request", "parent_id 必填")
		return
	}
	name, ok := validFolderName(req.Name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "folder_name_invalid", "文件夹名必须为 1–64 字符，且不能包含 / 或 ..")
		return
	}

	ctx := r.Context()
	if _, err := s.store.Folder(ctx, s.store.DB, req.ParentID); errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "folder_not_found", "父文件夹不存在")
		return
	} else if err != nil {
		s.internal(w, "读取父文件夹失败", err)
		return
	}

	f, err := s.store.CreateFolder(ctx, s.store.DB, req.ParentID, name)
	if errors.Is(err, db.ErrConflict) {
		writeErr(w, http.StatusConflict, "folder_name_taken", "同一父级下已存在同名文件夹")
		return
	}
	if err != nil {
		s.internal(w, "创建文件夹失败", err)
		return
	}
	writeJSON(w, http.StatusCreated, folderResponse{
		ID: f.ID, ParentID: f.ParentID, Name: f.Name, IsPublic: "inherit", Sort: f.Sort,
	})
}

func (s *Server) handlePatchFolder(w http.ResponseWriter, r *http.Request, id int64) {
	if id == 1 {
		writeErr(w, http.StatusBadRequest, "bad_request", "虚拟根不可修改")
		return
	}
	var req struct {
		Name     *string         `json:"name"`
		ParentID *int64          `json:"parent_id"`
		IsPublic json.RawMessage `json:"is_public"`
		Sort     *int64          `json:"sort"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "JSON 格式错误")
		return
	}

	ctx := r.Context()
	folders, err := s.store.Folders(ctx, s.store.DB)
	if err != nil {
		s.internal(w, "读取文件夹失败", err)
		return
	}
	byID := make(map[int64]db.Folder, len(folders))
	for _, f := range folders {
		byID[f.ID] = f
	}
	f, ok := byID[id]
	if !ok {
		writeErr(w, http.StatusNotFound, "folder_not_found", "文件夹不存在")
		return
	}

	if req.Name != nil {
		name, valid := validFolderName(*req.Name)
		if !valid {
			writeErr(w, http.StatusBadRequest, "folder_name_invalid", "文件夹名必须为 1–64 字符，且不能包含 / 或 ..")
			return
		}
		f.Name = name
	}
	if req.ParentID != nil {
		target := *req.ParentID
		if target == 0 {
			writeErr(w, http.StatusNotFound, "folder_not_found", "目标父文件夹不存在")
			return
		}
		if _, ok := byID[target]; !ok {
			writeErr(w, http.StatusNotFound, "folder_not_found", "目标父文件夹不存在")
			return
		}
		if cycleDetected(byID, id, target) {
			writeErr(w, http.StatusBadRequest, "cycle_detected", "不能把文件夹移动到自身或自己的子孙下")
			return
		}
		f.ParentID = target
	}
	if len(req.IsPublic) > 0 && string(req.IsPublic) != "null" {
		set, val, ok := parsePublic(req.IsPublic)
		if !ok {
			writeErr(w, http.StatusBadRequest, "bad_request", "is_public 只能是 1、0 或 \"inherit\"")
			return
		}
		if set {
			f.IsPublic = val
		}
	}
	if req.Sort != nil {
		f.Sort = *req.Sort
	}

	if err := s.store.UpdateFolder(ctx, s.store.DB, &f); errors.Is(err, db.ErrConflict) {
		writeErr(w, http.StatusConflict, "folder_name_taken", "同一父级下已存在同名文件夹")
		return
	} else if err != nil {
		s.internal(w, "更新文件夹失败", err)
		return
	}

	writeJSON(w, http.StatusOK, folderResponse{
		ID: f.ID, ParentID: f.ParentID, Name: f.Name, IsPublic: publicValue(f.IsPublic), Sort: f.Sort,
	})
}

func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request, id int64) {
	if id == 1 {
		writeErr(w, http.StatusBadRequest, "bad_request", "虚拟根不可删除")
		return
	}
	ctx := r.Context()
	if _, err := s.store.Folder(ctx, s.store.DB, id); errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "folder_not_found", "文件夹不存在")
		return
	} else if err != nil {
		s.internal(w, "读取文件夹失败", err)
		return
	}

	if r.URL.Query().Get("recursive") == "1" {
		folders, pages, err := s.store.DeleteFolderTree(ctx, id)
		if errors.Is(err, db.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "folder_not_found", "文件夹不存在")
			return
		}
		if err != nil {
			s.internal(w, "递归删除文件夹失败", err)
			return
		}
		s.log.Info("递归删除文件夹", "id", id, "folders", folders, "pages", pages)
		writeJSON(w, http.StatusOK, map[string]any{
			"deleted_folders": folders,
			"deleted_pages":   pages,
		})
		return
	}

	childFolders, childPages, err := s.store.FolderItemCount(ctx, s.store.DB, id)
	if err != nil {
		s.internal(w, "统计文件夹内容失败", err)
		return
	}
	if childFolders+childPages > 0 {
		writeErr(w, http.StatusConflict, "folder_not_empty",
			fmt.Sprintf("文件夹下仍有 %d 个子项，无法删除", childFolders+childPages))
		return
	}

	if err := s.store.DeleteFolder(ctx, s.store.DB, id); errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "folder_not_found", "文件夹不存在")
		return
	} else if err != nil {
		s.internal(w, "删除文件夹失败", err)
		return
	}
	writeNoContent(w)
}

func validFolderName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return "", false
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") || strings.ContainsRune(name, 0) {
		return "", false
	}
	return name, true
}

func cycleDetected(folders map[int64]db.Folder, self, target int64) bool {
	if target == self {
		return true
	}
	cur := target
	for depth := 0; depth < 64; depth++ {
		if cur == self {
			return true
		}
		if cur == 1 {
			return false
		}
		f, ok := folders[cur]
		if !ok {
			return true
		}
		cur = f.ParentID
	}
	return true
}

func parsePublic(raw json.RawMessage) (set bool, val *int, ok bool) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, nil, false
	}
	switch x := v.(type) {
	case float64:
		if x == 1 {
			n := 1
			return true, &n, true
		}
		if x == 0 {
			n := 0
			return true, &n, true
		}
	case string:
		if x == "inherit" {
			return true, nil, true
		}
	}
	return false, nil, false
}
