package admin

import (
	"errors"
	"net/http"

	"v2way/internal/gateway"
	"v2way/internal/store"
)

func storageFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrPending):
		respond(w, 503, map[string]any{"error": err.Error(), "code": "result_unknown", "status": "pending"})
	case errors.Is(err, store.ErrConflict):
		fail(w, 409, err.Error())
	case errors.Is(err, store.ErrNotFound):
		fail(w, 404, err.Error())
	case errors.Is(err, store.ErrReadOnly):
		fail(w, 503, err.Error())
	default:
		fail(w, 500, "存储操作失败，请检查数据目录状态")
	}
}
func (s *Server) importConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64  `json:"revision"`
		Document string `json:"document"`
	}
	if !decode(w, r, &in) {
		return
	}
	c, err := store.DecodeConfig([]byte(in.Document))
	if err != nil {
		fail(w, 422, "导入文件格式或内容无效："+err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.store.SaveDraft(c, in.Revision, requestSourceIP(r))
	result(w, d, err)
}
func (s *Server) confirmStorage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, err := s.store.PendingVersion()
	if errors.Is(err, store.ErrNotFound) {
		respond(w, 200, map[string]any{"status": "confirmed", "storage": s.store.Status()})
		return
	}
	if err != nil {
		storageFailure(w, err)
		return
	}
	snap, err := s.engine.Compile(candidate.Config)
	if err != nil {
		fail(w, 422, "待确认配置的运行资源校验失败："+err.Error())
		return
	}
	var plan *gateway.ListenerPlan
	if s.engine.Current().Version != candidate.ID && s.options.Listeners != nil {
		plan, err = s.options.Listeners.Prepare(snap)
		if err != nil {
			snap.Close()
			fail(w, 422, err.Error())
			return
		}
		defer plan.Abort()
	}
	if err = s.store.ConfirmPending(requestSourceIP(r)); err != nil {
		snap.Close()
		storageFailure(w, err)
		return
	}
	snap.Version = candidate.ID
	if s.engine.Current().Version != candidate.ID {
		if plan != nil {
			plan.Activate(snap)
		} else {
			s.engine.Activate(snap)
		}
	} else {
		snap.Close()
	}
	respond(w, 200, map[string]any{"status": "confirmed", "version": candidate.ID, "storage": s.store.Status()})
}
