package handler

import (
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"
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
	catRepo       *repository.CategoryRepository
	queue         *queue.ReminderQueue
}

func NewEventHandler(repo *repository.EventRepository, calRepo *repository.CalendarRepository, shareRepo *repository.CalendarShareRepository, inviteRepo *repository.InvitationRepository, recurringRepo *repository.RecurringEventRepository, catRepo *repository.CategoryRepository, q *queue.ReminderQueue) *EventHandler {
	return &EventHandler{repo: repo, calRepo: calRepo, shareRepo: shareRepo, inviteRepo: inviteRepo, recurringRepo: recurringRepo, catRepo: catRepo, queue: q}
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
	if !h.validateCategory(c, ownerID, req.CategoryID) {
		return
	}
	event, err := h.repo.Create(c.Request.Context(), ownerID, calendarID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(event.Reminders) > 0 {
		h.scheduleReminder(c, event, nil)
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

	// Occurrences are materialized a rolling window ahead (by the scheduler, hourly); a
	// range reaching past it gets its series' occurrences generated on demand. Best effort:
	// the list still works (just without those occurrences) if this fails.
	if to != nil && to.After(time.Now().Add((repository.WindowDays-1)*24*time.Hour)) {
		if err := h.recurringRepo.ExtendThrough(c.Request.Context(), ownerID, calendarID, *to); err != nil {
			log.Printf("extend recurring events through %s: %v", to.Format(time.RFC3339), err)
		}
	}

	// overlap=true also returns events that started before from but are still running
	// (multi-day all-day events); by default only events starting in range are listed.
	list := h.repo.List
	if c.Query("overlap") == "true" {
		list = h.repo.ListOverlapping
	}
	events, err := list(c.Request.Context(), ownerID, calendarID, from, to)
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
	before, err := h.repo.GetByID(c.Request.Context(), id, ownerID)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// A category is its author's Area: a changed one must be the event author's (a
	// calendar owner editing a collaborator's event can't file it under their own), and
	// an unchanged one — which the UI always sends back — isn't re-checked.
	if req.CategoryID.Set && !sameCategory(before.CategoryID, req.CategoryID.Value) &&
		!validateCategoryOwnership(c, h.catRepo, before.OwnerID, req.CategoryID.Value, "category not found") {
		return
	}
	event, err := h.updateEvent(c, id, ownerID, req, before)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, event)
}

func sameCategory(a, b *uuid.UUID) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// updateEvent applies req to the event (as it was: before) and re-syncs its reminders
// and invitations. Shared by PUT /events/:id and the scope "this" recurrence edit,
// which leave the same state.
func (h *EventHandler) updateEvent(c *gin.Context, id, ownerID uuid.UUID, req model.UpdateEventRequest, before *model.Event) (*model.Event, error) {
	ctx := c.Request.Context()
	event, err := h.repo.Update(ctx, id, ownerID, req)
	if err != nil {
		return nil, err
	}
	// Only once the update applied (the caller owns the event): the reminder queue is
	// keyed by event id alone, so cancelling first would let anyone who knows an id
	// wipe its reminders.
	if err := h.queue.Cancel(ctx, id); err != nil {
		log.Printf("cancel reminder %s: %v", id, err)
	}
	if len(event.Reminders) > 0 {
		h.scheduleReminder(c, event, before)
	}
	// Invite only attendees this edit added. The rest were invited when they were added
	// — or deliberately weren't, like the attendees of an imported event, which a
	// category change or drag must not suddenly email.
	var added []string
	for _, a := range event.Attendees {
		if !slices.Contains(before.Attendees, a) {
			added = append(added, a)
		}
	}
	if err := h.inviteRepo.UpsertForEvent(ctx, event.ID, added); err != nil {
		log.Printf("upsert invitations for event %s: %v", event.ID, err)
	}
	// Re-invite only when something the invitation shows changed — not for a category,
	// reminder or visibility change.
	if inviteDetailsChanged(before, event) {
		if err := h.inviteRepo.ReInviteChanged(ctx, event.ID, event.Attendees); err != nil {
			log.Printf("re-invite changed for event %s: %v", event.ID, err)
		}
	}
	if err := h.inviteRepo.RemoveAbsent(ctx, event.ID, event.Attendees); err != nil {
		log.Printf("remove absent invitations for event %s: %v", event.ID, err)
	}
	return event, nil
}

// inviteDetailsChanged reports whether an edit changed what an invitation shows.
func inviteDetailsChanged(before, after *model.Event) bool {
	return before.Title != after.Title || before.Location != after.Location ||
		before.Description != after.Description || before.AllDay != after.AllDay ||
		!before.StartTime.Equal(after.StartTime) || !before.EndTime.Equal(after.EndTime)
}

func (h *EventHandler) Delete(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	err = h.repo.Delete(c.Request.Context(), id, ownerID)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Only after the delete applied; see updateEvent.
	if err := h.queue.Cancel(c.Request.Context(), id); err != nil {
		log.Printf("cancel reminder %s: %v", id, err)
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

// validateCategory checks that a requested category exists and belongs to the caller.
// nil (no category / clear) is always valid. Writes the error response on failure.
func (h *EventHandler) validateCategory(c *gin.Context, ownerID uuid.UUID, categoryID *uuid.UUID) bool {
	return validateCategoryOwnership(c, h.catRepo, ownerID, categoryID, "category not found")
}

// UpdateRecurrence handles PUT /events/:id/recurrence.
// scope "this": apply changes to just that event, which detaches it from the series.
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
	if !h.validateCategory(c, ownerID, req.CategoryID) {
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
	// The modal sends every field, filled in from the occurrence opened. A series-wide
	// edit applies only what differs from it: a past occurrence keeps details that later
	// series edits changed, and sending those back would revert them.
	if req.Scope != "this" {
		dropUnchanged(&req, instance)
	}

	switch req.Scope {
	case "this":
		h.updateRecurrenceThis(c, ownerID, id, req, instance)
	case "this_and_following":
		h.updateRecurrenceThisAndFollowing(c, ownerID, *instance.RecurringEventID, instance.StartTime, req)
	case "all":
		h.updateRecurrenceAll(c, ownerID, *instance.RecurringEventID, instance.StartTime, req)
	}
}

// dropUnchanged clears the fields of req that inst already has. Start and end go
// together: a new start with the old end is a new duration.
func dropUnchanged(req *model.UpdateRecurrenceRequest, inst *model.Event) {
	if req.Title != nil && *req.Title == inst.Title {
		req.Title = nil
	}
	if req.Description != nil && *req.Description == inst.Description {
		req.Description = nil
	}
	if req.Location != nil && *req.Location == inst.Location {
		req.Location = nil
	}
	if (req.StartTime == nil || req.StartTime.Equal(inst.StartTime)) && (req.EndTime == nil || req.EndTime.Equal(inst.EndTime)) {
		req.StartTime, req.EndTime = nil, nil
	}
	if req.Attendees != nil && slices.Equal(req.Attendees, inst.Attendees) {
		req.Attendees = nil
	}
	if req.Reminders != nil && slices.Equal(req.Reminders, inst.Reminders) {
		req.Reminders = nil
	}
	if req.AllDay != nil && *req.AllDay == inst.AllDay {
		req.AllDay = nil
	}
	if req.Timezone != nil && *req.Timezone == inst.Timezone {
		req.Timezone = nil
	}
	if req.CategoryID != nil && sameCategory(req.CategoryID, inst.CategoryID) {
		req.CategoryID = nil
	}
	if req.Visibility != nil && *req.Visibility == inst.Visibility {
		req.Visibility = nil
	}
}

func (h *EventHandler) updateRecurrenceThis(c *gin.Context, ownerID, id uuid.UUID, req model.UpdateRecurrenceRequest, instance *model.Event) {
	// Update detaches the instance and records it as an exception on the series.
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
		Visibility:  req.Visibility,
	}
	// Recurrence updates use a plain pointer (nil = keep), so a category can
	// only be set here, not cleared.
	if req.CategoryID != nil {
		updateReq.CategoryID = model.Optional[uuid.UUID]{Set: true, Value: req.CategoryID}
	}
	updated, err := h.updateEvent(c, id, ownerID, updateReq, instance)
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
	if err == repository.ErrShortenedMonthOccurrence {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rec)
}

// DeleteRecurrence handles DELETE /events/:id/recurrence?scope=this|this_and_following|all.
// scope "this": delete just that instance (the series remembers not to regenerate it).
// scope "this_and_following": end the series before this instance; earlier ones are kept.
// scope "all": delete the series and every instance, past ones included.
func (h *EventHandler) DeleteRecurrence(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	scope := c.Query("scope")
	if scope != "this" && scope != "this_and_following" && scope != "all" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope must be one of this, this_and_following, all"})
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
	// An occurrence edited on its own is still part of its series; "this and following"
	// counts from the occurrence it was.
	seriesID, occurrence := instance.RecurringEventID, instance.StartTime
	if seriesID == nil && instance.DetachedFrom != nil && instance.OriginalStart != nil {
		seriesID, occurrence = instance.DetachedFrom, *instance.OriginalStart
	}
	if seriesID == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event is not part of a recurring series"})
		return
	}
	if instance.OwnerID != ownerID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	switch scope {
	case "this":
		if err = h.repo.Delete(ctx, id, ownerID); err == nil {
			if cerr := h.queue.Cancel(ctx, id); cerr != nil {
				log.Printf("cancel reminder %s: %v", id, cerr)
			}
		}
	case "this_and_following":
		err = h.recurringRepo.TruncateAt(ctx, *seriesID, ownerID, occurrence)
	case "all":
		err = h.recurringRepo.Delete(ctx, *seriesID, ownerID)
	}
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "recurring series not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// Stats handles GET /events/stats?from=&to=&tz= — time spent per Area (category),
// from categorized events and traces. Defaults to the trailing 7 days when a bound
// is omitted. For an "elapsed so far" view the caller passes to=now so future
// planned events don't count.
func (h *EventHandler) Stats(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)

	from, to, ok := statsRange(c, 7)
	if !ok {
		return
	}
	tz, ok := statsTZ(c)
	if !ok {
		return
	}

	stats, err := h.repo.Stats(c.Request.Context(), ownerID, from, to, tz)
	respondStats(c, stats, err)
}

// WeeklyStats is Stats split into weeks: GET /events/stats/weekly?from=&to=&tz=&week_start=
// gives each Area's minutes per week (of local days in tz, starting on week_start,
// Sun=0 … Sat=6, default Monday) over [from, to). to defaults to now, from to 8 weeks
// before to. The range may touch at most repository.MaxWeeklyStatsWeeks weeks.
func (h *EventHandler) WeeklyStats(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)

	from, to, ok := statsRange(c, 7*8)
	if !ok {
		return
	}
	if !from.Before(to) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "'from' must be before 'to'"})
		return
	}
	tz, ok := statsTZ(c)
	if !ok {
		return
	}
	weekStart := 1
	if v := c.Query("week_start"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 6 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'week_start', use 0 (Sunday) to 6 (Saturday)"})
			return
		}
		weekStart = n
	}

	stats, err := h.repo.WeeklyStats(c.Request.Context(), ownerID, from, to, tz, weekStart)
	if errors.Is(err, repository.ErrTooManyWeeks) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the range may touch at most " + strconv.Itoa(repository.MaxWeeklyStatsWeeks) + " weeks"})
		return
	}
	respondStats(c, stats, err)
}

// statsRange reads the stats' optional "from"/"to" (RFC3339): to defaults to now, and
// from to defaultDays before to. Returns false (after writing a 400) when either is invalid.
func statsRange(c *gin.Context, defaultDays int) (from, to time.Time, ok bool) {
	f, t, ok := parseRange(c)
	if !ok {
		return from, to, false
	}
	to = time.Now()
	if t != nil {
		to = *t
	}
	from = to.AddDate(0, 0, -defaultDays)
	if f != nil {
		from = *f
	}
	return from, to, true
}

// statsTZ reads the stats' optional "tz" query param (an IANA zone name, default
// UTC). Traces are counted by the day they're on, so the caller's time zone decides
// which days fall in a window. Returns false (after writing a 400) when it's invalid.
func statsTZ(c *gin.Context) (string, bool) {
	tz := c.DefaultQuery("tz", "UTC")
	if _, err := time.LoadLocation(tz); err != nil || tz == "" || tz == "Local" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'tz', use an IANA time zone name"})
		return "", false
	}
	return tz, true
}

// respondStats writes stats, or the error that came instead: a 400 for a time zone
// Postgres doesn't know, though Go's tz database does.
func respondStats(c *gin.Context, stats any, err error) {
	if errors.Is(err, repository.ErrInvalidTimeZone) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'tz', use an IANA time zone name"})
		return
	}
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

// scheduleReminder queues event's reminders. before is the event as it was before an edit
// (nil on create): a reminder still to come then, which the edit moved into the past, is
// sent now rather than never — Cancel has just removed it unsent.
func (h *EventHandler) scheduleReminder(c *gin.Context, event, before *model.Event) {
	now := time.Now()
	jobs := make([]queue.ReminderJob, 0, len(event.Reminders))
	for _, r := range event.Reminders {
		late := before != nil && before.StartTime.Add(-time.Duration(r.Minutes)*time.Minute).After(now) &&
			slices.ContainsFunc(before.Reminders, func(b model.Reminder) bool { return b.Minutes == r.Minutes })
		jobs = append(jobs, queue.ReminderJob{
			EventID:   event.ID,
			Minutes:   r.Minutes,
			Method:    r.Method,
			Title:     event.Title,
			StartTime: event.StartTime,
			Attendees: event.Attendees,
			Late:      late,
		})
	}
	if err := h.queue.Schedule(c.Request.Context(), jobs); err != nil {
		log.Printf("schedule reminders %s: %v", event.ID, err)
	}
}
