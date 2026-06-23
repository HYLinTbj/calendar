package handler

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/middleware"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/queue"
	"github.com/hylin/calendar/internal/repository"
	"github.com/jackc/pgx/v5"
)

type EventHandler struct {
	repo          *repository.EventRepository
	calRepo       *repository.CalendarRepository
	shareRepo     *repository.CalendarShareRepository
	inviteRepo    *repository.InvitationRepository
	recurringRepo *repository.RecurringEventRepository
	queue         *queue.ReminderQueue
}

func NewEventHandler(repo *repository.EventRepository, calRepo *repository.CalendarRepository, shareRepo *repository.CalendarShareRepository, inviteRepo *repository.InvitationRepository, recurringRepo *repository.RecurringEventRepository, q *queue.ReminderQueue) *EventHandler {
	return &EventHandler{repo: repo, calRepo: calRepo, shareRepo: shareRepo, inviteRepo: inviteRepo, recurringRepo: recurringRepo, queue: q}
}

func (h *EventHandler) Create(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	var req model.CreateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	calendarID, ok := h.resolveCalendar(c, ownerID, req.CalendarID)
	if !ok {
		return
	}
	event, err := h.repo.Create(c.Request.Context(), ownerID, calendarID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(event.Reminders) > 0 {
		h.scheduleReminder(c, event)
	}
	if err := h.inviteRepo.UpsertForEvent(c.Request.Context(), event.ID, event.Attendees); err != nil {
		log.Printf("upsert invitations for event %s: %v", event.ID, err)
	}
	c.JSON(http.StatusCreated, event)
}

func (h *EventHandler) GetByID(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	event, err := h.repo.GetByID(c.Request.Context(), id, ownerID)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Attach per-attendee RSVP statuses when there are invitees.
	// maskIfPrivate already clears Attendees for masked events, so this is a no-op for "Busy" events.
	if len(event.Attendees) > 0 {
		statuses, err := h.inviteRepo.ListStatusesByEvent(c.Request.Context(), event.ID)
		if err == nil {
			event.AttendeeStatuses = statuses
		}
	}
	c.JSON(http.StatusOK, event)
}

func (h *EventHandler) List(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)

	var calendarID *uuid.UUID
	if v := c.Query("calendar_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid calendar_id"})
			return
		}
		calendarID = &id
	}

	var from, to *time.Time
	if v := c.Query("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'from', use RFC3339"})
			return
		}
		from = &t
	}
	if v := c.Query("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'to', use RFC3339"})
			return
		}
		to = &t
	}

	events, err := h.repo.List(c.Request.Context(), ownerID, calendarID, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, events)
}

func (h *EventHandler) Search(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)

	q := c.Query("q")
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "q is required"})
		return
	}

	var calendarID *uuid.UUID
	if v := c.Query("calendar_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid calendar_id"})
			return
		}
		calendarID = &id
	}

	events, err := h.repo.Search(c.Request.Context(), ownerID, q, calendarID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, events)
}

func (h *EventHandler) Update(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req model.UpdateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Validate the target calendar is accessible (owned or edit share) before moving.
	if req.CalendarID != nil {
		if _, ok := h.resolveCalendar(c, ownerID, req.CalendarID); !ok {
			return
		}
	}
	if err := h.queue.Cancel(c.Request.Context(), id); err != nil {
		log.Printf("cancel reminder %s: %v", id, err)
	}
	event, err := h.repo.Update(c.Request.Context(), id, ownerID, req)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(event.Reminders) > 0 {
		h.scheduleReminder(c, event)
	}
	if err := h.inviteRepo.UpsertForEvent(c.Request.Context(), event.ID, event.Attendees); err != nil {
		log.Printf("upsert invitations for event %s: %v", event.ID, err)
	}
	if err := h.inviteRepo.ReInviteChanged(c.Request.Context(), event.ID, event.Attendees); err != nil {
		log.Printf("re-invite changed for event %s: %v", event.ID, err)
	}
	if err := h.inviteRepo.RemoveAbsent(c.Request.Context(), event.ID, event.Attendees); err != nil {
		log.Printf("remove absent invitations for event %s: %v", event.ID, err)
	}
	c.JSON(http.StatusOK, event)
}

func (h *EventHandler) Delete(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.queue.Cancel(c.Request.Context(), id); err != nil {
		log.Printf("cancel reminder %s: %v", id, err)
	}
	if err := h.repo.Delete(c.Request.Context(), id, ownerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// resolveCalendar returns the target calendar ID: the one requested (owned or with edit share),
// or the user's default calendar.
func (h *EventHandler) resolveCalendar(c *gin.Context, ownerID uuid.UUID, requested *uuid.UUID) (uuid.UUID, bool) {
	if requested != nil {
		_, err := h.calRepo.GetByID(c.Request.Context(), *requested, ownerID)
		if err == nil {
			return *requested, true
		}
		if err != pgx.ErrNoRows {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return uuid.UUID{}, false
		}
		// Not the owner — check for edit share.
		perm, err := h.shareRepo.GetPermission(c.Request.Context(), *requested, ownerID)
		if err != nil || perm != "edit" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "calendar not found"})
			return uuid.UUID{}, false
		}
		return *requested, true
	}
	def, err := h.calRepo.GetDefault(c.Request.Context(), ownerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not resolve default calendar"})
		return uuid.UUID{}, false
	}
	return def.ID, true
}

// UpdateRecurrence handles PUT /events/:id/recurrence.
// scope "this": detach instance, apply changes to just that event.
// scope "this_and_following": truncate series at pivot, create new series with changes.
// scope "all": update the series template (and shift anchor if start_time changed).
func (h *EventHandler) UpdateRecurrence(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req model.UpdateRecurrenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	instance, err := h.repo.GetByID(ctx, id, ownerID)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if instance.RecurringEventID == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event is not part of a recurring series"})
		return
	}
	if instance.OwnerID != ownerID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	switch req.Scope {
	case "this":
		h.updateRecurrenceThis(c, ownerID, id, req)
	case "this_and_following":
		h.updateRecurrenceThisAndFollowing(c, ownerID, *instance.RecurringEventID, instance.StartTime, req)
	case "all":
		h.updateRecurrenceAll(c, ownerID, *instance.RecurringEventID, instance.StartTime, req)
	}
}

func (h *EventHandler) updateRecurrenceThis(c *gin.Context, ownerID, id uuid.UUID, req model.UpdateRecurrenceRequest) {
	ctx := c.Request.Context()
	if err := h.repo.DetachInstance(ctx, id, ownerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	updateReq := model.UpdateEventRequest{
		Title:       req.Title,
		Description: req.Description,
		Location:    req.Location,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Attendees:   req.Attendees,
		Reminders:   req.Reminders,
		AllDay:      req.AllDay,
		Timezone:    req.Timezone,
		CategoryID:  req.CategoryID,
		Visibility:  req.Visibility,
	}
	updated, err := h.repo.Update(ctx, id, ownerID, updateReq)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (h *EventHandler) updateRecurrenceThisAndFollowing(c *gin.Context, ownerID, recurringEventID uuid.UUID, pivot time.Time, req model.UpdateRecurrenceRequest) {
	rec, err := h.recurringRepo.SplitAt(c.Request.Context(), recurringEventID, ownerID, pivot, req)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "recurring series not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rec)
}

func (h *EventHandler) updateRecurrenceAll(c *gin.Context, ownerID, recurringEventID uuid.UUID, instanceStartTime time.Time, req model.UpdateRecurrenceRequest) {
	rec, err := h.recurringRepo.UpdateAll(c.Request.Context(), recurringEventID, ownerID, instanceStartTime, req)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "recurring series not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rec)
}

// Stats handles GET /events/stats?from=&to= — time spent per Area (category),
// derived from categorized events. Defaults to the trailing 7 days when a bound
// is omitted. For an "elapsed so far" view the caller passes to=now so future
// planned events don't count.
func (h *EventHandler) Stats(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)

	from, to, ok := parseRange(c)
	if !ok {
		return
	}
	toVal := time.Now()
	if to != nil {
		toVal = *to
	}
	fromVal := toVal.AddDate(0, 0, -7)
	if from != nil {
		fromVal = *from
	}

	stats, err := h.repo.Stats(c.Request.Context(), ownerID, fromVal, toVal)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

// parseRange reads optional RFC3339 "from"/"to" query params. Returns false
// (after writing a 400) when either is present but unparseable.
func parseRange(c *gin.Context) (from, to *time.Time, ok bool) {
	if v := c.Query("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'from', use RFC3339"})
			return nil, nil, false
		}
		from = &t
	}
	if v := c.Query("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'to', use RFC3339"})
			return nil, nil, false
		}
		to = &t
	}
	return from, to, true
}

func (h *EventHandler) scheduleReminder(c *gin.Context, event *model.Event) {
	jobs := make([]queue.ReminderJob, 0, len(event.Reminders))
	for _, r := range event.Reminders {
		jobs = append(jobs, queue.ReminderJob{
			EventID:   event.ID,
			Minutes:   r.Minutes,
			Method:    r.Method,
			Title:     event.Title,
			StartTime: event.StartTime,
			Attendees: event.Attendees,
		})
	}
	if err := h.queue.Schedule(c.Request.Context(), jobs); err != nil {
		log.Printf("schedule reminders %s: %v", event.ID, err)
	}
}
