// Package upload handles user-uploaded files: avatar images and post attachments.
package upload

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const (
	MaxAvatarBytes  int64 = 2 * 1024 * 1024 // 2 MB
	AvatarDimension       = 256
)

// AvatarResult holds the persisted avatar's URL path.
type AvatarResult struct {
	URL string // e.g. "/uploads/avatars/42.jpg"
}

// AttachmentResult holds metadata about a saved attachment file.
type AttachmentResult struct {
	StoragePath string // relative to uploadDir, e.g. "attachments/2026/05/{uuid}.jpg"
	Filename    string // original filename from the upload
	ContentType string
	SizeBytes   int64
}

// SaveAttachment validates and stores an uploaded file.
// It checks the MIME type against allowedTypes, enforces maxBytes, then writes
// the file to uploadDir/attachments/{year}/{month}/{uuid}.{ext}.
func SaveAttachment(r io.Reader, originalFilename string, allowedTypes []string, maxBytes int64, uploadDir string) (*AttachmentResult, error) {
	sniff := make([]byte, 512)
	n, err := io.ReadFull(r, sniff)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("read attachment header: %w", err)
	}
	sniff = sniff[:n]
	mimeType := http.DetectContentType(sniff)

	ext, ok := extForMIME(mimeType, allowedTypes)
	if !ok {
		return nil, fmt.Errorf("file type %q is not allowed", mimeType)
	}

	limited := io.LimitReader(io.MultiReader(bytes.NewReader(sniff), r), maxBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("file exceeds the %d MB size limit", maxBytes/(1<<20))
	}

	now := time.Now()
	dir := filepath.Join(uploadDir, "attachments",
		strconv.Itoa(now.Year()),
		fmt.Sprintf("%02d", now.Month()),
	)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create attachment directory: %w", err)
	}

	storedName := uuid.New().String() + "." + ext
	dst := filepath.Join(dir, storedName)
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		return nil, fmt.Errorf("write attachment: %w", err)
	}

	relPath := filepath.Join("attachments",
		strconv.Itoa(now.Year()),
		fmt.Sprintf("%02d", now.Month()),
		storedName,
	)

	return &AttachmentResult{
		StoragePath: filepath.ToSlash(relPath),
		Filename:    originalFilename,
		ContentType: mimeType,
		SizeBytes:   int64(len(raw)),
	}, nil
}

// extForMIME returns the file extension for mimeType if it appears in allowedTypes.
func extForMIME(mimeType string, allowedTypes []string) (string, bool) {
	allowed := false
	for _, t := range allowedTypes {
		if t == mimeType {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", false
	}
	switch mimeType {
	case "image/jpeg":
		return "jpg", true
	case "image/png":
		return "png", true
	case "image/gif":
		return "gif", true
	case "image/webp":
		return "webp", true
	case "application/pdf":
		return "pdf", true
	case "application/zip":
		return "zip", true
	}
	return "", false
}

// SaveAvatar reads from r, detects MIME type, validates size, center-crops,
// scales to AvatarDimension×AvatarDimension, and stores the result at
// uploadDir/avatars/{userID}.{ext}. Accepts JPEG, PNG, and GIF (first frame).
func SaveAvatar(r io.Reader, userID int64, uploadDir string) (*AvatarResult, error) {
	// Sniff the first 512 bytes for MIME detection.
	sniff := make([]byte, 512)
	n, err := io.ReadFull(r, sniff)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("read avatar header: %w", err)
	}
	sniff = sniff[:n]
	mimeType := http.DetectContentType(sniff)

	ext, ok := allowedAvatarExt(mimeType)
	if !ok {
		return nil, fmt.Errorf("unsupported image type %q: please upload a JPEG, PNG, or GIF", mimeType)
	}

	// Buffer the full upload, capped at MaxAvatarBytes+1 to detect overflow.
	limited := io.LimitReader(io.MultiReader(bytes.NewReader(sniff), r), MaxAvatarBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read avatar: %w", err)
	}
	if int64(len(raw)) > MaxAvatarBytes {
		return nil, fmt.Errorf("image exceeds the %d MB size limit", MaxAvatarBytes/(1<<20))
	}

	img, err := decodeImage(mimeType, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	resized := cropAndScale(img, AvatarDimension, AvatarDimension)

	avatarDir := filepath.Join(uploadDir, "avatars")
	if err = os.MkdirAll(avatarDir, 0o755); err != nil {
		return nil, fmt.Errorf("create avatars directory: %w", err)
	}

	filename := strconv.FormatInt(userID, 10) + "." + ext
	dst := filepath.Join(avatarDir, filename)
	f, err := os.Create(dst)
	if err != nil {
		return nil, fmt.Errorf("create avatar file: %w", err)
	}
	defer f.Close()

	if err = encodeImage(f, resized, ext); err != nil {
		return nil, fmt.Errorf("encode avatar: %w", err)
	}

	return &AvatarResult{URL: "/uploads/avatars/" + filename}, nil
}

func allowedAvatarExt(mimeType string) (string, bool) {
	switch mimeType {
	case "image/jpeg":
		return "jpg", true
	case "image/png":
		return "png", true
	case "image/gif":
		return "gif", true
	}
	return "", false
}

func decodeImage(mimeType string, r io.Reader) (image.Image, error) {
	switch mimeType {
	case "image/jpeg":
		return jpeg.Decode(r)
	case "image/png":
		return png.Decode(r)
	case "image/gif":
		g, err := gif.DecodeAll(r)
		if err != nil {
			return nil, err
		}
		return g.Image[0], nil // first frame only
	}
	return nil, fmt.Errorf("no decoder for %q", mimeType)
}

func encodeImage(w io.Writer, img image.Image, ext string) error {
	switch ext {
	case "jpg":
		return jpeg.Encode(w, img, &jpeg.Options{Quality: 90})
	case "png":
		return png.Encode(w, img)
	case "gif":
		return gif.Encode(w, toNRGBA(img), nil)
	}
	return fmt.Errorf("no encoder for ext %q", ext)
}

// cropAndScale center-crops src to a square region, then scales it to w×h
// using nearest-neighbor interpolation.
func cropAndScale(src image.Image, w, h int) image.Image {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()

	// Determine the largest centered square crop.
	cs := sw
	if sh < cs {
		cs = sh
	}
	cx := sb.Min.X + (sw-cs)/2
	cy := sb.Min.Y + (sh-cs)/2

	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			sx := cx + dx*cs/w
			sy := cy + dy*cs/h
			dst.Set(dx, dy, src.At(sx, sy))
		}
	}
	return dst
}

// toNRGBA converts any image.Image to *image.NRGBA so the GIF encoder
// receives a concrete type it can work with.
func toNRGBA(src image.Image) *image.NRGBA {
	if n, ok := src.(*image.NRGBA); ok {
		return n
	}
	b := src.Bounds()
	dst := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			rv, gv, bv, a := src.At(x, y).RGBA()
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(rv >> 8),
				G: uint8(gv >> 8),
				B: uint8(bv >> 8),
				A: uint8(a >> 8),
			})
		}
	}
	return dst
}
