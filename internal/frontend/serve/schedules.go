package serve

import (
	"encoding/json"
	"errors"
	"net/http"

	"reasonix/internal/runtime/scheduler"
	"reasonix/internal/session/control"
	"reasonix/internal/state/schedule"
)

func (s *Server) registerScheduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /schedules", s.schedules)
	mux.HandleFunc("GET /schedules/result", s.scheduleResult)
	mux.HandleFunc("POST /schedules/preview", s.previewSchedule)
	mux.HandleFunc("POST /schedules", s.createSchedule)
	mux.HandleFunc("POST /schedules/pause-all", s.scheduleSwitch(true))
	mux.HandleFunc("POST /schedules/resume-all", s.scheduleSwitch(false))
	mux.HandleFunc("POST /schedules/{id}/pause", s.scheduleOne("pause"))
	mux.HandleFunc("POST /schedules/{id}/resume", s.scheduleOne("resume"))
	mux.HandleFunc("POST /schedules/{id}/renew", s.scheduleOne("renew"))
	mux.HandleFunc("POST /schedules/{id}/delete", s.scheduleOne("delete"))
}

// allowSchedules gates what widens unattended spending, which a networked
// client or a paired phone must not decide for the machine's owner. Pausing and
// deleting only narrow it, and stay open to any reader.
func (s *Server) allowSchedules(w http.ResponseWriter, r *http.Request) bool {
	if s.grants.at(r).providerEdit {
		return true
	}
	refuse(w, http.StatusForbidden, "schedule.editing_disabled", "scheduled runs are not editable by this client", nil)
	return false
}

func (s *Server) schedules(w http.ResponseWriter, r *http.Request) {
	board, err := s.ctl().Schedules(r.Context())
	if err != nil {
		refuseSchedule(w, err)
		return
	}
	writeJSON(w, board)
}

func (s *Server) scheduleResult(w http.ResponseWriter, r *http.Request) {
	trigger := r.URL.Query().Get("trigger")
	if trigger == "" {
		missingField(w, "trigger")
		return
	}
	res, err := s.ctl().ScheduleResult(r.Context(), trigger)
	if err != nil {
		refuseSchedule(w, err)
		return
	}
	writeJSON(w, res)
}

func (s *Server) previewSchedule(w http.ResponseWriter, r *http.Request) {
	var draft control.ScheduleDraft
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&draft); err != nil {
		badBody(w)
		return
	}
	prev, err := s.ctl().PreviewSchedule(r.Context(), draft)
	if err != nil {
		refuseSchedule(w, err)
		return
	}
	writeJSON(w, prev)
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.allowSchedules(w, r) {
		return
	}
	var body struct {
		control.ScheduleDraft
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&body); err != nil {
		badBody(w)
		return
	}
	if body.Confirm == "" {
		refuse(w, http.StatusConflict, "schedule.confirmation_required", "a schedule is created only from a preview the person confirmed", nil)
		return
	}
	sc, err := s.ctl().CreateSchedule(r.Context(), body.ScheduleDraft, body.Confirm)
	if err != nil {
		refuseSchedule(w, err)
		return
	}
	writeJSON(w, sc)
}

func (s *Server) scheduleSwitch(paused bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !paused && !s.allowSchedules(w, r) {
			return
		}
		if err := s.ctl().SetSchedulesPaused(r.Context(), paused); err != nil {
			refuseSchedule(w, err)
			return
		}
		s.schedules(w, r)
	}
}

func (s *Server) scheduleOne(verb string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if (verb == "resume" || verb == "renew") && !s.allowSchedules(w, r) {
			return
		}
		id, ctx, ctl := r.PathValue("id"), r.Context(), s.ctl()
		var err error
		switch verb {
		case "pause":
			err = ctl.PauseSchedule(ctx, id)
		case "resume":
			err = ctl.ResumeSchedule(ctx, id)
		case "renew":
			err = ctl.RenewSchedule(ctx, id)
		case "delete":
			err = ctl.DeleteSchedule(ctx, id)
		}
		if err != nil {
			refuseSchedule(w, err)
			return
		}
		s.schedules(w, r)
	}
}

// refuseSchedule gives each refusal its own code: the store and the controller
// answer by identity, and the sentence is the frontend's.
func refuseSchedule(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, schedule.ErrNotFound), errors.Is(err, schedule.ErrResultNotFound):
		refuse(w, http.StatusNotFound, "schedule.not_found", "no such schedule or result", nil)
	case errors.Is(err, control.ErrScheduleConfirmMismatch):
		refuse(w, http.StatusConflict, "schedule.digest_mismatch", "what is being created is not what was previewed", nil)
	case errors.Is(err, schedule.ErrIntervalBelowFloor):
		refuse(w, http.StatusBadRequest, "schedule.interval_too_short", "the interval is under the minimum", nil)
	case errors.Is(err, schedule.ErrTooMany):
		refuse(w, http.StatusConflict, "schedule.limit_reached", "the schedule limit is reached", nil)
	case errors.Is(err, scheduler.ErrDraftTrigger):
		refuse(w, http.StatusBadRequest, "schedule.bad_trigger", "give exactly one of an interval and a time", nil)
	case errors.Is(err, scheduler.ErrDraftFolderUnknown):
		refuse(w, http.StatusBadRequest, "schedule.workspace_unknown", "the folder is not a trusted project", nil)
	case errors.Is(err, scheduler.ErrDraftWorkspace):
		refuse(w, http.StatusBadRequest, "schedule.no_folder", "the folder does not exist", nil)
	case errors.Is(err, scheduler.ErrDraftModel):
		refuse(w, http.StatusBadRequest, "schedule.model_unconfigured", "the model is not configured", nil)
	case errors.Is(err, schedule.ErrInvalid):
		refuse(w, http.StatusBadRequest, "schedule.invalid", "the schedule is not valid", nil)
	case errors.Is(err, schedule.ErrSchemaReadonly):
		refuse(w, http.StatusConflict, "schedule.schema_readonly", "the schedules were written by a newer version and are read-only here", nil)
	case errors.Is(err, schedule.ErrNotActive), errors.Is(err, schedule.ErrExpired):
		refuse(w, http.StatusConflict, "schedule.not_active", "the schedule is not active", nil)
	default:
		saveFailed(w, http.StatusInternalServerError, "schedule.save_failed", err)
	}
}
