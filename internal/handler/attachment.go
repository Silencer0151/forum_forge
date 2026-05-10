package handler

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
	"github.com/nitro/forum_forge/internal/upload"
)

const defaultMaxAttachments = 5

// AttachmentHandlers serves and accepts file attachments on posts.
type AttachmentHandlers struct {
	store      store.Store
	uploadPath string
}

// NewAttachmentHandler constructs an AttachmentHandlers.
func NewAttachmentHandler(st store.Store, uploadPath string) *AttachmentHandlers {
	return &AttachmentHandlers{store: st, uploadPath: uploadPath}
}

// ServeAttachment handles GET /attachments/{id}.
// It looks up the attachment record, opens the file from disk, and serves it
// with the correct Content-Type and Content-Disposition headers.
func (h *AttachmentHandlers) ServeAttachment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	att, err := h.store.GetAttachmentByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get attachment", "id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	fullPath := filepath.Join(h.uploadPath, filepath.FromSlash(att.StoragePath))
	f, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		slog.Error("open attachment file", "path", fullPath, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		slog.Error("stat attachment file", "path", fullPath, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	disposition := "attachment"
	if strings.HasPrefix(att.ContentType, "image/") {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", att.ContentType)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`%s; filename="%s"`, disposition, att.Filename))

	http.ServeContent(w, r, att.Filename, stat.ModTime(), f)
}

// SavePostAttachments processes multipart file uploads for a post, saves them
// to disk, and creates the attachment records in the store.
// It returns the list of saved attachments on success. Partial failures are
// logged and skipped rather than aborting the whole upload batch.
func (h *AttachmentHandlers) SavePostAttachments(r *http.Request, post *model.Post, settings *model.Settings) ([]*model.Attachment, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}

	maxSize := settings.MaxAttachmentSizeBytes
	if maxSize <= 0 {
		maxSize = 5 * 1024 * 1024
	}
	allowedTypes := settings.AllowedAttachmentTypes
	if len(allowedTypes) == 0 {
		allowedTypes = model.DefaultSettings().AllowedAttachmentTypes
	}

	files := r.MultipartForm.File["attachments"]
	if len(files) == 0 {
		return nil, nil
	}

	maxCount := defaultMaxAttachments
	if len(files) > maxCount {
		files = files[:maxCount]
	}

	ctx := r.Context()
	var saved []*model.Attachment

	for _, fh := range files {
		if fh.Size == 0 {
			continue
		}
		f, err := fh.Open()
		if err != nil {
			slog.Warn("open uploaded file", "filename", fh.Filename, "error", err)
			continue
		}

		result, err := upload.SaveAttachment(f, fh.Filename, allowedTypes, maxSize, h.uploadPath)
		f.Close()
		if err != nil {
			slog.Warn("save attachment", "filename", fh.Filename, "error", err)
			continue
		}

		att := &model.Attachment{
			PostID:      post.ID,
			Filename:    result.Filename,
			StoragePath: result.StoragePath,
			ContentType: result.ContentType,
			SizeBytes:   result.SizeBytes,
		}
		if err := h.store.CreateAttachment(ctx, att); err != nil {
			slog.Error("create attachment record", "post_id", post.ID, "error", err)
			continue
		}
		saved = append(saved, att)
	}

	return saved, nil
}

// requireLogin redirects unauthenticated requests to the login page.
func requireLogin(w http.ResponseWriter, r *http.Request) bool {
	if middleware.UserFromContext(r.Context()) == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return true
	}
	return false
}
