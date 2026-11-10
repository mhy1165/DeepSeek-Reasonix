package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"reasonix/internal/contract/tool"
	"reasonix/internal/session/control"
	"reasonix/internal/tools/jobs"
)

func (s *Server) registerJobRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /jobs/{id}/cancel", s.cancelJob)
	mux.HandleFunc("POST /jobs/{id}/restart", s.restartJob)
	mux.HandleFunc("POST /jobs/{id}/register", s.registerTaskCommand)
	mux.HandleFunc("POST /task-commands/{id}/start", s.startTaskCommand)
	mux.HandleFunc("POST /task-commands/{id}/delete", s.removeTaskCommand)
	mux.HandleFunc("GET /jobs/{id}/output", s.jobOutput)
}

func (s *Server) startTaskCommand(w http.ResponseWriter, r *http.Request) {
	view, err := s.ctl().StartTaskCommand(r.Context(), r.PathValue("id"))
	switch {
	case err == nil:
		writeJSON(w, view)
	case errors.Is(err, control.ErrTaskCommandNotFound):
		refuse(w, http.StatusConflict, "task_command.not_found", "no such task command in this session", nil)
	case errors.Is(err, control.ErrTaskCommandRunning):
		refuse(w, http.StatusConflict, "task_command.running", "this task command is already running", nil)
	case errors.Is(err, control.ErrTaskCommandUnavailable):
		refuse(w, http.StatusConflict, "task_command.unavailable", "task commands are unavailable here", nil)
	default:
		refuse(w, http.StatusConflict, "task_command.start_failed", "the task command could not be started", nil)
	}
}

func (s *Server) registerTaskCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			refuse(w, http.StatusRequestEntityTooLarge, "task_command.request_too_large", "the registration request is too large", nil)
			return
		}
		refuse(w, http.StatusBadRequest, "task_command.invalid", "the registration request is malformed", nil)
		return
	}
	id, err := s.ctl().RegisterTaskCommand(r.Context(), tool.TaskCommandRegistration{
		JobID: r.PathValue("id"), Name: body.Name, Overwrite: body.Overwrite,
	})
	switch {
	case err == nil:
		writeJSON(w, map[string]string{"id": id})
	case errors.Is(err, control.ErrTaskCommandExists):
		refuse(w, http.StatusConflict, "task_command.exists", "a task command with this id already exists", nil)
	case errors.Is(err, control.ErrTaskCommandInvalid):
		refuse(w, http.StatusConflict, "task_command.invalid", "the task command registration is invalid", nil)
	case errors.Is(err, control.ErrTaskCommandNoDurableSession):
		refuse(w, http.StatusConflict, "task_command.no_durable_session", "task commands require a durable session", nil)
	default:
		refuse(w, http.StatusInternalServerError, "task_command.register_failed", "the task command could not be registered", nil)
		return
	}
}

// removeTaskCommand deletes one registered definition from this session. The
// job it may have started is untouched, which is why a still-running command is
// refused rather than deleted out from under its process.
func (s *Server) removeTaskCommand(w http.ResponseWriter, r *http.Request) {
	err := s.ctl().RemoveTaskCommand(r.Context(), r.PathValue("id"))
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, control.ErrTaskCommandNotFound):
		refuse(w, http.StatusConflict, "task_command.not_found", "no such task command in this session", nil)
	case errors.Is(err, control.ErrTaskCommandRunning):
		refuse(w, http.StatusConflict, "task_command.remove_running", "stop this task command before deleting it", nil)
	case errors.Is(err, control.ErrTaskCommandUnavailable):
		refuse(w, http.StatusConflict, "task_command.unavailable", "task commands are unavailable here", nil)
	default:
		refuse(w, http.StatusInternalServerError, "task_command.remove_failed", "the task command could not be removed", nil)
	}
}

func (s *Server) restartJob(w http.ResponseWriter, r *http.Request) {
	view, err := s.ctl().RestartJob(r.PathValue("id"))
	switch {
	case err == nil:
		writeJSON(w, view)
	case errors.Is(err, jobs.ErrRestartNotRunning):
		refuse(w, http.StatusConflict, "job.not_running", "no running background job with this id in this session", nil)
	case errors.Is(err, jobs.ErrRestartUnavailable):
		refuse(w, http.StatusConflict, "job.not_restartable", "this background job has no restart recipe", nil)
	case errors.Is(err, jobs.ErrRestartInProgress):
		refuse(w, http.StatusConflict, "job.restart_in_progress", "the background job is already being restarted", nil)
	case errors.Is(err, jobs.ErrRestartTimeout):
		refuse(w, http.StatusConflict, "job.restart_timeout", "the old background job did not stop before the restart deadline", nil)
	default:
		refuse(w, http.StatusInternalServerError, "job.restart_failed", "the background job could not be restarted", nil)
	}
}

// cancelJob stops one of this session's background jobs. A job that already
// ended, or belongs to another session, is refused rather than acknowledged:
// the panel is showing a row that no longer answers to it.
func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	ctl, id := s.ctl(), r.PathValue("id")
	if !ctl.CancelJob(id) {
		// A job the kill was already asked for stays on the running list until its
		// process tree has gone, so "listed, and the kill refuses" is that wait.
		for _, j := range ctl.Jobs() {
			if j.ID == id {
				refuse(w, http.StatusConflict, "job.stop_in_progress", "the stop was already requested; the job is still shutting down", nil)
				return
			}
		}
		refuse(w, http.StatusConflict, "job.not_running", "no running background job with this id in this session", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// JobOutput is the end of a running background job's output as the panel shows
// it: the text, how much the job has written in all, and whether the text is
// only the last part of that.
type JobOutput struct {
	Text      string `json:"text"`
	Status    string `json:"status"`
	Bytes     int64  `json:"bytes"`
	Truncated bool   `json:"truncated"`
}

const (
	jobOutputDefault = 16 << 10
	jobOutputMax     = 64 << 10
)

// jobOutput lets the person read what a background job has printed. It is a
// look, not a read: the model's own reader keeps everything it has not seen.
func (s *Server) jobOutput(w http.ResponseWriter, r *http.Request) {
	limit := jobOutputDefault
	if raw := r.URL.Query().Get("max"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(n, jobOutputMax)
		}
	}
	peek, ok := s.ctl().JobPeek(r.PathValue("id"), limit)
	if !ok {
		refuse(w, http.StatusConflict, "job.not_running", "no background job with this id in this session", nil)
		return
	}
	writeJSON(w, JobOutput{Text: peek.Text, Status: string(peek.Status), Bytes: peek.Bytes, Truncated: peek.Truncated})
}
