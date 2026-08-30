package server

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// maxAttachment is what one upload may weigh. Generous for a screenshot or a
// log, small enough that a repository does not become a file server by
// accident — git is bad at large binaries and stays bad forever, since history
// keeps every version.
const maxAttachment = 25 << 20 // 25 MB

var embeddable = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true,
}

// handleAttach saves an uploaded file into the vault and records it on the task.
func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)

	if err := r.ParseMultipartForm(maxAttachment); err != nil {
		s.fail(w, r, http.StatusBadRequest, "That did not arrive",
			"The upload was rejected: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "No file", "Choose a file to attach.")
		return
	}
	defer file.Close()

	if header.Size > maxAttachment {
		s.fail(w, r, http.StatusBadRequest, "Too large", fmt.Sprintf(
			"%s is %.1f MB. The limit is %d MB, because every version of a file stays in git "+
				"forever and a repository is a poor file server.",
			header.Filename, float64(header.Size)/(1<<20), maxAttachment>>20))
		return
	}

	name := attachmentName(key, header.Filename)
	rel := filepath.ToSlash(filepath.Join(vault.Attachments, name))
	author := s.authorFor(r)

	s.writes.Lock()
	saved, err := s.saveAttachment(rel, file)
	if err != nil {
		s.writes.Unlock()
		s.fail(w, r, http.StatusInternalServerError, "Not saved", err.Error())
		return
	}
	s.writes.Unlock()

	// The file and the line that points at it land in one commit: half of an
	// attachment is worse than none.
	embed := embeddable[strings.ToLower(filepath.Ext(saved))]
	err = s.editTask(key, "", author, func(t *task.Task) (string, []string, error) {
		t.AppendAttachment(saved, embed)
		return key + ": attached " + filepath.Base(saved), []string{saved}, nil
	})
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Saved, but not recorded", err.Error())
		return
	}

	http.Redirect(w, r, "/task/"+key, http.StatusSeeOther)
}

// attachmentName keeps the uploaded name but prefixes the key, so two tasks
// can both attach "screenshot.png" and the folder still says which is which.
func attachmentName(key, original string) string {
	original = filepath.Base(strings.ReplaceAll(original, "\\", "/"))
	original = strings.TrimSpace(original)
	if original == "" || original == "." || original == ".." {
		original = "attachment"
	}
	return key + " " + original
}

func (s *Server) saveAttachment(rel string, src io.Reader) (string, error) {
	full, err := s.abs(rel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}

	// O_EXCL, then a numbered suffix: an upload must never silently replace a
	// file something else already points at.
	name, path := rel, full
	for n := 2; ; n++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			defer f.Close()
			if _, err := io.Copy(f, src); err != nil {
				return "", err
			}
			return name, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
		ext := filepath.Ext(rel)
		name = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(rel, ext), n, ext)
		if path, err = s.abs(name); err != nil {
			return "", err
		}
	}
}

// handleDeleteTask removes a task. git keeps it; the vault does not.
func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	rel, full, err := s.locate(key)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}

	// A parent that vanishes leaves its children pointing at nothing, which
	// rule 5 would report. Say so before doing it, not after.
	if children := s.childrenOf(c, key); len(children) > 0 {
		s.fail(w, r, http.StatusBadRequest, "It still has children", fmt.Sprintf(
			"%s is the parent of %d task(s). Move them somewhere else first, or they will "+
				"point at nothing.", key, len(children)))
		return
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	if err := os.Remove(full); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Not deleted", err.Error())
		return
	}
	if err := s.commit([]string{rel}, "deleted "+key, s.authorFor(r)); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Deleted, but not committed", err.Error())
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleFile serves an attachment. Only from the attachments folder: the vault
// is a repository, and the rest of it is not the web server's to hand out.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	rel := path.Clean("/" + r.PathValue("path"))[1:]
	if !strings.HasPrefix(rel, vault.Attachments+"/") || strings.Contains(rel, "..") {
		http.NotFound(w, r)
		return
	}

	// An attachment is somebody else's file served from this server's origin.
	// An .svg or .html among them, opened directly, is a page running script
	// with the reader's session — anybody who may attach a file could otherwise
	// take over the account of anybody who clicks it.
	//
	// The sandbox neuters the document without stopping an <img> from loading
	// it, which is how images are embedded. Anything that is not an image the
	// interface embeds is sent as a download rather than rendered, because
	// there is no reason to render it and every reason not to.
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !embeddable[strings.ToLower(path.Ext(rel))] {
		w.Header().Set("Content-Disposition",
			"attachment; filename*=UTF-8''"+url.PathEscape(path.Base(rel)))
	}

	full, err := s.abs(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, full)
}
