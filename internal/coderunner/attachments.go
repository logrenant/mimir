package coderunner

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrAttachmentNotFound means no attachment has that id.
var ErrAttachmentNotFound = errors.New("coderunner: no such attachment")

// ErrAttachmentType means the bytes are not one of the image formats a coding
// agent can read. A distinct sentinel because the fix is the operator's
// ("attach an image"), not the machine's (SD-6).
var ErrAttachmentType = errors.New("coderunner: unsupported attachment type")

// attachmentSweepAge is how long an uploaded file survives without a run
// referencing it. A composer can be open for a while before the task is
// created, so this is generous; the sweep only ever runs at startup.
const attachmentSweepAge = 24 * time.Hour

// imageTypes is the whole allow-list, and it is an allow-list on purpose. The
// path an attachment takes ends with `claude` opening the file, so what may be
// written here is decided by what a coding agent can usefully read — not by
// what a client claims to be sending.
var imageTypes = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/gif":  "gif",
	"image/webp": "webp",
}

// Attachment is one uploaded image, described by what the daemon determined
// rather than by what the client said.
type Attachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MIME     string `json:"mime"`
	Bytes    int    `json:"bytes"`

	// Ext is the extension the daemon chose from the sniffed type. It is not
	// part of the wire shape: the client never needs it, and letting a client
	// influence it is how a filename becomes a path.
	Ext string `json:"-"`
}

func newAttachmentID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("coderunner: generating attachment id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// sniffImage decides the type from the bytes themselves.
//
// http.DetectContentType reads magic numbers, so a .png that is really a shell
// script is rejected here rather than landing on disk with a name that invites
// something to run it. The extension comes from this result and never from the
// client's filename, which is kept only to show the operator what they picked.
func sniffImage(data []byte) (mime, ext string, err error) {
	detected := http.DetectContentType(data)
	// DetectContentType may append parameters; the type is what matters.
	if i := strings.IndexByte(detected, ';'); i >= 0 {
		detected = strings.TrimSpace(detected[:i])
	}
	ext, ok := imageTypes[detected]
	if !ok {
		return "", "", fmt.Errorf("%w: %s (png, jpeg, gif and webp are accepted)",
			ErrAttachmentType, detected)
	}
	return detected, ext, nil
}

// sanitizeFilename keeps a display name that cannot be mistaken for a path.
func sanitizeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == ".." || name == string(filepath.Separator) {
		name = ""
	}
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}

func (r *Runner) attachmentFile(a Attachment) string {
	return filepath.Join(r.cfg.AttachmentDir, a.ID+"."+a.Ext)
}

func (r *Runner) attachmentMeta(id string) string {
	return filepath.Join(r.cfg.AttachmentDir, id+".json")
}

// SaveAttachment validates and stores one image, returning what it became.
//
// The metadata sidecar exists because the id alone does not say which extension
// the file has, and reconstructing that by globbing the directory would make
// the id a pattern rather than a key.
func (r *Runner) SaveAttachment(filename string, data []byte) (Attachment, error) {
	if len(data) == 0 {
		return Attachment{}, fmt.Errorf("%w: the file is empty", ErrAttachmentType)
	}
	mime, ext, err := sniffImage(data)
	if err != nil {
		return Attachment{}, err
	}
	id, err := newAttachmentID()
	if err != nil {
		return Attachment{}, err
	}
	if err := os.MkdirAll(r.cfg.AttachmentDir, 0o755); err != nil {
		return Attachment{}, fmt.Errorf("coderunner: creating attachment directory: %w", err)
	}

	att := Attachment{
		ID:       id,
		Filename: sanitizeFilename(filename),
		MIME:     mime,
		Bytes:    len(data),
		Ext:      ext,
	}
	if err := os.WriteFile(r.attachmentFile(att), data, 0o600); err != nil {
		return Attachment{}, fmt.Errorf("coderunner: writing attachment: %w", err)
	}

	meta, err := json.Marshal(struct {
		Attachment
		Ext string `json:"ext"`
	}{Attachment: att, Ext: ext})
	if err != nil {
		return Attachment{}, fmt.Errorf("coderunner: encoding attachment metadata: %w", err)
	}
	if err := os.WriteFile(r.attachmentMeta(id), meta, 0o600); err != nil {
		return Attachment{}, fmt.Errorf("coderunner: writing attachment metadata: %w", err)
	}
	return att, nil
}

// statAttachment reads an attachment's metadata without its bytes.
func (r *Runner) statAttachment(id string) (Attachment, error) {
	if !isHexID(id) {
		return Attachment{}, fmt.Errorf("%w: %s", ErrAttachmentNotFound, id)
	}
	raw, err := os.ReadFile(r.attachmentMeta(id))
	if err != nil {
		return Attachment{}, fmt.Errorf("%w: %s", ErrAttachmentNotFound, id)
	}
	var meta struct {
		Attachment
		Ext string `json:"ext"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return Attachment{}, fmt.Errorf("%w: %s", ErrAttachmentNotFound, id)
	}
	att := meta.Attachment
	att.Ext = meta.Ext
	att.ID = id
	return att, nil
}

// LoadAttachment returns an attachment and its bytes.
func (r *Runner) LoadAttachment(id string) (Attachment, []byte, error) {
	att, err := r.statAttachment(id)
	if err != nil {
		return Attachment{}, nil, err
	}
	data, err := os.ReadFile(r.attachmentFile(att))
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("%w: %s", ErrAttachmentNotFound, id)
	}
	return att, data, nil
}

// attachmentPaths resolves ids to on-disk paths, skipping any that no longer
// exist. A missing attachment must not stop a run: the prompt is still worth
// answering, and the operator can see which images the run reported reading.
func (r *Runner) attachmentPaths(ids []string) []string {
	var out []string
	for _, id := range ids {
		att, err := r.statAttachment(id)
		if err != nil {
			continue
		}
		path := r.attachmentFile(att)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		out = append(out, path)
	}
	return out
}

// isHexID guards the one place an id becomes part of a filename. Ids are
// generated here and are always hex, so anything else is a client experimenting
// with the path, not a lookup that could succeed.
func isHexID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// encodeAttachmentIDs renders the ids for the run row. Empty stays empty rather
// than becoming "[]", so "has no attachments" is one value in the column.
func encodeAttachmentIDs(ids []string) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return "", fmt.Errorf("coderunner: encoding attachment ids: %w", err)
	}
	return string(raw), nil
}

// decodeAttachmentIDs is the inverse, and is deliberately forgiving: a row
// whose column cannot be parsed is a row with no attachments, not a run that
// fails to load.
func decodeAttachmentIDs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil
	}
	return ids
}

// SweepAttachments deletes stored images that no run refers to and that are old
// enough to be certain no composer is still holding them.
//
// Called once at startup. An upload happens before the task that will own it
// exists, so there is always a window in which a file is legitimately
// unreferenced — the age check is what keeps the sweep from deleting into it.
func (r *Runner) SweepAttachments(referenced []string, now time.Time) (int, error) {
	keep := map[string]struct{}{}
	for _, raw := range referenced {
		for _, id := range decodeAttachmentIDs(raw) {
			keep[id] = struct{}{}
		}
	}

	entries, err := os.ReadDir(r.cfg.AttachmentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("coderunner: reading attachment directory: %w", err)
	}

	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		id := strings.TrimSuffix(name, filepath.Ext(name))
		if _, ok := keep[id]; ok {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < attachmentSweepAge {
			continue
		}
		if err := os.Remove(filepath.Join(r.cfg.AttachmentDir, name)); err == nil {
			removed++
		}
	}
	return removed, nil
}

// removeAttachments deletes one run's images outright, used when the run itself
// is deleted. Ids shared with another run are left alone.
func (r *Runner) removeAttachments(ids []string, keep map[string]struct{}) {
	for _, id := range ids {
		if _, shared := keep[id]; shared {
			continue
		}
		att, err := r.statAttachment(id)
		if err != nil {
			continue
		}
		_ = os.Remove(r.attachmentFile(att))
		_ = os.Remove(r.attachmentMeta(id))
	}
}
