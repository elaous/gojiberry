package public

import (
	"context"
	"errors"
	"github.com/elaous/gojiberry/docstore"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type docResponse struct {
	// ID is the document's ID, for the owner.
	ID string `json:"id"`
	// ViewID names the read-only link.
	ViewID   string    `json:"viewId"`
	Expires  time.Time `json:"expires"`
	Versions int       `json:"versions"`
}

var errTooSlow = errors.New("public: the request body arrives slower than the minimum rate")

type rateReader struct {
	r     io.Reader
	rc    *http.ResponseController
	start time.Time
	read  int64
	grace time.Duration
	rate  int64         // bytes per second
	limit time.Duration // the whole body, from the start
}

func (m *rateReader) Read(p []byte) (int, error) {
	allowed := min(m.grace+time.Duration(m.read*int64(time.Second)/m.rate), m.limit)
	if time.Since(m.start) >= allowed {
		return 0, errTooSlow
	}
	_ = m.rc.SetReadDeadline(m.start.Add(allowed))
	n, err := m.r.Read(p)
	m.read += int64(n)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		err = errTooSlow
	}
	return n, err
}

func (s *Service) readBody(w http.ResponseWriter, r *http.Request, tk *ticket) ([]byte, bool) {
	if r.ContentLength > s.cfg.MaxDocBytes {
		http.Error(w, "document too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	var src io.Reader = r.Body
	if s.cfg.MinUploadRate > 0 && (r.ContentLength < 0 || r.ContentLength >= MinRateBodyBytes) {
		src = &rateReader{r: r.Body, rc: http.NewResponseController(w), start: time.Now(), grace: s.cfg.UploadGrace, rate: s.cfg.MinUploadRate, limit: s.cfg.RequestTimeout}
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, struct {
		io.Reader
		io.Closer
	}{src, r.Body}, s.cfg.MaxDocBytes))
	if err != nil {
		tk.keep()
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "document too large", http.StatusRequestEntityTooLarge)
			return nil, false
		}
		if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, errTooSlow) {
			w.Header().Set("Connection", "close")
			http.Error(w, "the request body took too long to arrive", http.StatusRequestTimeout)
			return nil, false
		}
		http.Error(w, "reading the request body failed", http.StatusBadRequest)
		return nil, false
	}
	if len(data) == 0 {
		http.Error(w, "an empty document", http.StatusBadRequest)
		return nil, false
	}
	return data, true
}

func (s *Service) screen(w http.ResponseWriter, r *http.Request, data []byte, tk *ticket) bool {
	bad, err := s.blocklist.Blocked(r.Context(), data)
	if err != nil {
		log.Printf("public docstore: blocklist: %v", err)
		http.Error(w, "content check unavailable", http.StatusServiceUnavailable)
		return false
	}
	if bad {
		tk.keep()
		http.Error(w, "this content is not accepted", http.StatusUnprocessableEntity)
		return false
	}
	return true
}

func (s *Service) handleDoc(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createDoc(w, r)
	case http.MethodPut:
		s.editDoc(w, r)
	case http.MethodGet, http.MethodHead:
		s.downloadDoc(w, r)
	case http.MethodDelete:
		s.discardDoc(w, r)
	default:
		methodNotAllowed(w, "POST, PUT, GET, DELETE")
	}
}

func (s *Service) createDoc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key, sess, ok := s.session(w, r)
	if !ok {
		return
	}
	if err := s.challenge.Verify(r); err != nil {
		s.refuseChallenge(w, err)
		return
	}
	if !s.writeAllowed(w, r, sess) {
		return
	}
	old := sess.DocID
	if old != "" {
		if gone, err := s.removed(ctx, old); err != nil {
			s.fail(w, err)
			return
		} else if gone {
			http.Error(w, "this session's document was removed; the session cannot create another", http.StatusForbidden)
			return
		}
		if live, err := s.live(ctx, old); err != nil {
			s.fail(w, err)
			return
		} else if live {
			http.Error(w, "this session already has a document: download or discard it first", http.StatusConflict)
			return
		}
	}
	tk, ok := s.admit(w, r)
	if !ok {
		return
	}
	defer tk.release()
	data, ok := s.readBody(w, r, tk)
	if !ok {
		return
	}
	tk.settle(int64(len(data)))
	if !s.screen(w, r, data, tk) {
		return
	}
	n := int64(len(data))
	docID, err := randomString(16)
	if err != nil {
		s.fail(w, err)
		return
	}
	res, fits := s.bytes.reserve(docID, n, n, s.cfg.MaxTotalBytes)
	if !fits {
		storeFull(w)
		return
	}
	defer res.cancel() // a no-op once committed
	switch claimed, err := s.sessions.SetDoc(ctx, key, old, docID, s.now()); {
	case errors.Is(err, ErrNoSession):
		http.Error(w, "the session ended", http.StatusUnauthorized)
		return
	case err != nil:
		s.fail(w, err)
		return
	case !claimed:
		http.Error(w, "this session already has a document: download or discard it first", http.StatusConflict)
		return
	}
	release := func() { _, _ = s.sessions.SetDoc(context.WithoutCancel(ctx), key, docID, old, s.now()) }
	unlock := s.lock(docID)
	defer unlock()
	view := viewIDOf(docID)
	meta := docMeta{Owner: sess.User().ID, ViewID: view, Created: s.now().UTC(), Versions: 1, Bytes: len(data)}
	rollback := func() {
		bg := context.WithoutCancel(ctx)
		_ = s.deleteKey(bg, docKey(docID))
		_ = s.dropRecords(bg, docID)
		s.bytes.release(docID) // a reconcile may have listed the document meanwhile
		release()
	}
	if err := s.putJSON(ctx, docRecordPrefix+docID, meta); err != nil {
		rollback()
		s.fail(w, err)
		return
	}
	if err := s.putJSON(ctx, viewRecordPrefix+view, viewRecord{Doc: docID}); err != nil {
		rollback()
		s.fail(w, err)
		return
	}
	if err := s.store.Save(ctx, docKey(docID), data); err != nil {
		rollback()
		s.fail(w, err)
		return
	}
	exp, has, err := s.expiresOf(ctx, docID)
	if err != nil {
		rollback()
		s.fail(w, err)
		return
	}
	if !has {
		rollback()
		s.alarmNoDeadline()
		s.fail(w, errNoDeadline)
		return
	}
	if gone, err := s.removed(ctx, docID); err != nil {
		rollback()
		s.fail(w, err)
		return
	} else if gone {
		bg := context.WithoutCancel(ctx)
		_ = s.deleteKey(bg, docKey(docID))
		_ = s.dropRecords(bg, docID)
		s.bytes.release(docID)
		http.Error(w, "this document was removed", http.StatusGone)
		return
	}
	res.commit()
	tk.keep()
	s.renewSession(ctx, key, sess)
	w.Header().Set("X-Gojiberry-Expires", exp.Format(time.RFC3339))
	writeJSON(w, http.StatusCreated, docResponse{ID: docID, ViewID: view, Expires: exp, Versions: 1})
}

func (s *Service) ownDoc(w http.ResponseWriter, r *http.Request, sess Session) (docID string, ok bool) {
	ctx := r.Context()
	if sess.DocID == "" {
		http.Error(w, "this session has no document", http.StatusNotFound)
		return "", false
	}
	if gone, err := s.removed(ctx, sess.DocID); err != nil {
		s.fail(w, err)
		return "", false
	} else if gone {
		http.Error(w, "this document was removed", http.StatusGone)
		return "", false
	}
	return sess.DocID, true
}

func (s *Service) editDoc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key, sess, ok := s.session(w, r)
	if !ok {
		return
	}
	docID, ok := s.ownDoc(w, r, sess)
	if !ok {
		return
	}
	if !s.writeAllowed(w, r, sess) {
		return
	}
	var early docMeta
	if found, err := s.getJSON(ctx, docRecordPrefix+docID, &early); err != nil {
		s.fail(w, err)
		return
	} else if found && early.Versions >= s.cfg.MaxVersions {
		http.Error(w, "this document has reached its limit of saves; download it and start a new one", http.StatusForbidden)
		return
	}
	tk, ok := s.admit(w, r)
	if !ok {
		return
	}
	defer tk.release()
	data, ok := s.readBody(w, r, tk)
	if !ok {
		return
	}
	tk.settle(int64(len(data)))
	if !s.screen(w, r, data, tk) {
		return
	}
	unlock := s.lock(docID)
	defer unlock()
	var meta docMeta
	if found, err := s.getJSON(ctx, docRecordPrefix+docID, &meta); err != nil {
		s.fail(w, err)
		return
	} else if !found || meta.Owner != sess.User().ID {
		http.Error(w, "this session has no document", http.StatusNotFound)
		return
	}
	if live, err := s.live(ctx, docID); err != nil {
		s.fail(w, err)
		return
	} else if !live {
		http.Error(w, "this document expired; create a new one", http.StatusNotFound)
		return
	}
	if meta.Versions >= s.cfg.MaxVersions {
		http.Error(w, "this document has reached its limit of saves; download it and start a new one", http.StatusForbidden)
		return
	}
	delta := int64(len(data)) - int64(meta.Bytes)
	res, fits := s.bytes.reserve(docID, int64(len(data)), delta, s.cfg.MaxTotalBytes)
	if !fits {
		storeFull(w)
		return
	}
	defer res.cancel() // a no-op once committed
	next := meta
	next.Versions++
	next.Bytes = len(data)
	if err := s.putJSON(ctx, docRecordPrefix+docID, next); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.store.Save(ctx, docKey(docID), data); err != nil {
		_ = s.putJSON(context.WithoutCancel(ctx), docRecordPrefix+docID, meta)
		s.fail(w, err)
		return
	}
	exp, has, err := s.expiresOf(ctx, docID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !has {
		bg := context.WithoutCancel(ctx)
		_ = s.deleteKey(bg, docKey(docID))
		_ = s.dropRecords(bg, docID)
		s.bytes.release(docID)
		s.alarmNoDeadline()
		s.fail(w, errNoDeadline)
		return
	}
	res.commit()
	tk.keep()
	s.renewSession(ctx, key, sess)
	w.Header().Set("X-Gojiberry-Expires", exp.Format(time.RFC3339))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) downloadDoc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, sess, ok := s.session(w, r)
	if !ok {
		return
	}
	docID, ok := s.ownDoc(w, r, sess)
	if !ok {
		return
	}
	doc, err := s.store.Load(ctx, docKey(docID))
	if errors.Is(err, docstore.ErrNotFound) {
		http.Error(w, "this document expired or was deleted", http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="document"`)
	s.setExpires(ctx, w, docID)
	var meta docMeta
	if found, err := s.getJSON(ctx, docRecordPrefix+docID, &meta); err == nil && found && meta.ViewID != "" {
		w.Header().Set("X-Gojiberry-View-Id", meta.ViewID)
	}
	_, _ = w.Write(doc.Data)
}

func (s *Service) discardDoc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key, sess, ok := s.session(w, r)
	if !ok {
		return
	}
	docID, ok := s.ownDoc(w, r, sess)
	if !ok {
		return
	}
	unlock := s.lock(docID)
	defer unlock()
	if err := s.deleteKey(ctx, docKey(docID)); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.dropRecords(ctx, docID); err != nil {
		s.fail(w, err)
		return
	}
	s.bytes.release(docID)
	if _, err := s.sessions.SetDoc(ctx, key, docID, "", s.now()); err != nil && !errors.Is(err, ErrNoSession) {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	if !s.allow(w, r, s.lim.View, "view", "") {
		return
	}
	ctx := r.Context()
	view := strings.TrimPrefix(r.URL.Path, "/public/view/")
	if !validID(view) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var t tombstone
	if gone, err := s.getJSON(ctx, tombPrefix+view, &t); err != nil {
		s.fail(w, err)
		return
	} else if gone {
		http.Error(w, "this document was removed", http.StatusGone)
		return
	}
	var rec viewRecord
	if found, err := s.getJSON(ctx, viewRecordPrefix+view, &rec); err != nil {
		s.fail(w, err)
		return
	} else if !found || !validID(rec.Doc) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	doc, err := s.store.Load(ctx, docKey(rec.Doc))
	if errors.Is(err, docstore.ErrNotFound) {
		_ = s.dropRecords(context.WithoutCancel(ctx), rec.Doc)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	s.setExpires(ctx, w, rec.Doc)
	_, _ = w.Write(doc.Data)
}

func (s *Service) resolve(ctx context.Context, id string) (docID, view string, ok bool, err error) {
	if !validID(id) {
		return "", "", false, nil
	}
	var rec viewRecord
	if found, err := s.getJSON(ctx, viewRecordPrefix+id, &rec); err != nil {
		return "", "", false, err
	} else if found && validID(rec.Doc) {
		return rec.Doc, id, true, nil
	}
	var meta docMeta
	if found, err := s.getJSON(ctx, docRecordPrefix+id, &meta); err != nil {
		return "", "", false, err
	} else if found {
		return id, viewIDOf(id), true, nil
	}
	return "", "", false, nil
}
