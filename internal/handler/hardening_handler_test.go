//go:build integration

package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeID(t *testing.T, w *httptest.ResponseRecorder) uuid.UUID {
	t.Helper()
	var v struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&v))
	return v.ID
}

func futureEvent(extra map[string]interface{}) map[string]interface{} {
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute)
	body := map[string]interface{}{
		"title":      "Planning",
		"start_time": start.Format(time.RFC3339),
		"end_time":   start.Add(time.Hour).Format(time.RFC3339),
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func TestEventHandler_OthersCannotDeleteOrWipeReminders(t *testing.T) {
	truncateAll(t, testPool)
	_, owner := MustRegisterAndLogin(t, testRouter, "rem_owner")
	_, other := MustRegisterAndLogin(t, testRouter, "rem_other")
	ctx := context.Background()

	w := Do(t, testRouter, "POST", "/events", owner, futureEvent(map[string]interface{}{
		"reminders": []map[string]interface{}{{"minutes": 30, "method": "email"}},
	}))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	id := decodeID(t, w)
	queued := func() int64 { n, err := testRDB.ZCard(ctx, "reminders").Result(); require.NoError(t, err); return n }
	require.Equal(t, int64(1), queued())

	// Someone who doesn't own the event gets a 404 and leaves its reminder alone.
	assert.Equal(t, http.StatusNotFound, Do(t, testRouter, "DELETE", "/events/"+id.String(), other, nil).Code)
	assert.Equal(t, http.StatusNotFound, Do(t, testRouter, "PUT", "/events/"+id.String(), other, map[string]string{"title": "x"}).Code)
	assert.Equal(t, int64(1), queued(), "the owner's reminder survives others' attempts")
	assert.Equal(t, http.StatusOK, Do(t, testRouter, "GET", "/events/"+id.String(), owner, nil).Code)

	// The owner's delete does cancel it.
	assert.Equal(t, http.StatusNoContent, Do(t, testRouter, "DELETE", "/events/"+id.String(), owner, nil).Code)
	assert.Equal(t, int64(0), queued())
	assert.Equal(t, http.StatusNotFound, Do(t, testRouter, "DELETE", "/events/"+id.String(), owner, nil).Code, "already gone")
}

func TestEventHandler_RejectsInvalidAttendees(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "attendees")

	for name, attendees := range map[string][]string{
		"not an address": {"not-an-email"},
		"line break":     {"ok@example.com\r\nBcc: x@evil.test"},
		"more than 100 of them": func() []string {
			a := make([]string, 101)
			for i := range a {
				a[i] = fmt.Sprintf("guest%d@example.com", i)
			}
			return a
		}(),
	} {
		w := Do(t, testRouter, "POST", "/events", token, futureEvent(map[string]interface{}{"attendees": attendees}))
		assert.Equal(t, http.StatusBadRequest, w.Code, "%s: %s", name, w.Body.String())
		w = Do(t, testRouter, "POST", "/recurring-events", token, futureEvent(map[string]interface{}{
			"attendees": attendees, "frequency": "weekly",
		}))
		assert.Equal(t, http.StatusBadRequest, w.Code, "recurring, %s: %s", name, w.Body.String())
	}
	w := Do(t, testRouter, "POST", "/events", token, futureEvent(map[string]interface{}{"attendees": []string{"guest@example.com"}}))
	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

func TestUserHandler_ProfileAndPasswordValidation(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "profile")

	for _, body := range []map[string]string{
		{"email": "", "current_password": "password123"},
		{"email": "bob", "current_password": "password123"},
		{"username": ""},
		{"password": strings.Repeat("a", 73), "current_password": "password123"},
	} {
		w := Do(t, testRouter, "PUT", "/users/me", token, body)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%v: %s", body, w.Body.String())
	}
	w := Do(t, testRouter, "PUT", "/users/me", token, map[string]string{"password": "a-new-password", "current_password": "password123"})
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

	w = Do(t, testRouter, "POST", "/auth/register", "", map[string]string{
		"username": "longpw", "email": "longpw@example.com", "password": strings.Repeat("p", 73),
	})
	assert.Equal(t, http.StatusBadRequest, w.Code, "over 72 bytes is a 400, not a 500: %s", w.Body.String())
}

func TestUserHandler_DeleteAccountWithCollaboratorEvents(t *testing.T) {
	truncateAll(t, testPool)
	_, alice := MustRegisterAndLogin(t, testRouter, "del_alice")
	_, bob := MustRegisterAndLogin(t, testRouter, "del_bob")

	w := Do(t, testRouter, "GET", "/calendars", alice, nil)
	var cals []struct {
		ID        uuid.UUID `json:"id"`
		IsDefault bool      `json:"is_default"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&cals))
	require.NotEmpty(t, cals)
	calID := cals[0].ID

	w = Do(t, testRouter, "POST", fmt.Sprintf("/calendars/%s/shares", calID), alice, map[string]string{
		"email": "user_del_bob@example.com", "permission": "edit",
	})
	require.Less(t, w.Code, 300, w.Body.String())
	// Bob puts an event in Alice's calendar (collaborators can add events, not series).
	w = Do(t, testRouter, "POST", "/events", bob, futureEvent(map[string]interface{}{"calendar_id": calID}))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	bobsEvent := decodeID(t, w)

	w = Do(t, testRouter, "DELETE", "/users/me", alice, nil)
	assert.Equal(t, http.StatusNoContent, w.Code, "blocked by the collaborator's rows before: %s", w.Body.String())
	assert.Equal(t, http.StatusNotFound, Do(t, testRouter, "GET", "/events/"+bobsEvent.String(), bob, nil).Code, "went with Alice's calendar")
	assert.Equal(t, http.StatusOK, Do(t, testRouter, "GET", "/users/me", bob, nil).Code)
}

func TestCalendarHandler_DeleteCalendarWithSeries(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "delcal_series")

	w := Do(t, testRouter, "POST", "/calendars", token, map[string]string{"name": "Gym"})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	gym := decodeID(t, w)
	w = Do(t, testRouter, "POST", "/recurring-events", token, futureEvent(map[string]interface{}{"calendar_id": gym, "frequency": "weekly"}))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	series := decodeID(t, w)

	w = Do(t, testRouter, "DELETE", "/calendars/"+gym.String(), token, nil)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	w = Do(t, testRouter, "GET", "/recurring-events/"+series.String(), token, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var rec struct {
		CalendarID uuid.UUID `json:"calendar_id"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rec))
	assert.NotEqual(t, gym, rec.CalendarID, "the series moved to the default calendar")
}

func TestEventHandler_ReInvitesOnlyWhenDetailsChange(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "reinvite")
	catID := createCategory(t, token, "Work", 0)
	ctx := context.Background()

	w := Do(t, testRouter, "POST", "/events", token, futureEvent(map[string]interface{}{"attendees": []string{"guest@example.com"}}))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	id := decodeID(t, w)
	status := func() string {
		var s string
		require.NoError(t, testPool.QueryRow(ctx, `SELECT status FROM event_invitations WHERE event_id = $1`, id).Scan(&s))
		return s
	}
	_, err := testPool.Exec(ctx, `UPDATE event_invitations SET status = 'sent' WHERE event_id = $1`, id)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, Do(t, testRouter, "PUT", "/events/"+id.String(), token, map[string]interface{}{"category_id": catID}).Code)
	assert.Equal(t, "sent", status(), "re-categorizing isn't news to the guest")
	require.Equal(t, http.StatusOK, Do(t, testRouter, "PUT", "/events/"+id.String(), token, map[string]interface{}{"title": "Planning, moved"}).Code)
	assert.Equal(t, "pending_send", status(), "a new title is re-sent")

	// An invitation that failed (e.g. during a mail outage) is revived by the next change,
	// with a fresh set of attempts — and shown as failed until then.
	_, err = testPool.Exec(ctx, `UPDATE event_invitations SET status = 'failed', attempts = 5, last_error = 'x' WHERE event_id = $1`, id)
	require.NoError(t, err)
	w = Do(t, testRouter, "GET", "/events/"+id.String(), token, nil)
	require.Contains(t, w.Body.String(), `"status":"failed"`)
	require.Equal(t, http.StatusOK, Do(t, testRouter, "PUT", "/events/"+id.String(), token, map[string]interface{}{"location": "Room 2"}).Code)
	assert.Equal(t, "pending_send", status())
	var attempts int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT attempts FROM event_invitations WHERE event_id = $1`, id).Scan(&attempts))
	assert.Zero(t, attempts)
}

func TestICSHandler_ImportDoesNotInvite(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "ics_noinvite")
	ctx := context.Background()

	w := Do(t, testRouter, "GET", "/calendars", token, nil)
	var cals []struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&cals))
	start := time.Now().UTC().Add(72 * time.Hour).Format("20060102T150405Z")
	end := time.Now().UTC().Add(73 * time.Hour).Format("20060102T150405Z")
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" +
		"BEGIN:VEVENT\r\nUID:one\r\nDTSTART:" + start + "\r\nDTEND:" + end + "\r\nSUMMARY:One-off\r\n" +
		"ATTENDEE:mailto:guest@example.com\r\nATTENDEE:mailto:not an address\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:series\r\nDTSTART:" + start + "\r\nDTEND:" + end + "\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:Weekly\r\n" +
		"ATTENDEE:mailto:guest@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	req := httptest.NewRequest("POST", fmt.Sprintf("/calendars/%s/import", cals[0].ID), strings.NewReader(ics))
	req.Header.Set("Content-Type", "text/calendar")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var invitations int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM event_invitations`).Scan(&invitations))
	assert.Zero(t, invitations, "importing doesn't email anyone")
	var attendees []string
	require.NoError(t, testPool.QueryRow(ctx, `SELECT attendees FROM events WHERE title = 'One-off'`).Scan(&attendees))
	assert.Equal(t, []string{"guest@example.com"}, attendees, "attendees are kept; the invalid one is dropped")

	// Nor do the series' occurrences the scheduler generates later.
	_, err := testPool.Exec(ctx, `UPDATE recurring_events SET generated_until = start_time`)
	require.NoError(t, err)
	w = Do(t, testRouter, "GET", "/events?from="+time.Now().UTC().Format(time.RFC3339)+"&to="+time.Now().UTC().Add(200*24*time.Hour).Format(time.RFC3339), token, nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM event_invitations`).Scan(&invitations))
	assert.Zero(t, invitations)

	// Nor does editing an imported event later: a category change on the one-off, or a
	// "this event" edit of an imported series' occurrence.
	catID := createCategory(t, token, "Meetings", 0)
	var oneOff, occurrence uuid.UUID
	require.NoError(t, testPool.QueryRow(ctx, `SELECT id FROM events WHERE title = 'One-off'`).Scan(&oneOff))
	require.NoError(t, testPool.QueryRow(ctx, `SELECT id FROM events WHERE title = 'Weekly' ORDER BY start_time LIMIT 1`).Scan(&occurrence))
	require.Equal(t, http.StatusOK, Do(t, testRouter, "PUT", "/events/"+oneOff.String(), token, map[string]interface{}{"category_id": catID}).Code)
	w = Do(t, testRouter, "PUT", "/events/"+occurrence.String()+"/recurrence", token, map[string]interface{}{"scope": "this", "category_id": catID})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM event_invitations`).Scan(&invitations))
	assert.Zero(t, invitations, "the importer's edits don't email the imported attendees")

	// An attendee the user adds is invited — only that one.
	w = Do(t, testRouter, "PUT", "/events/"+oneOff.String(), token, map[string]interface{}{"attendees": []string{"guest@example.com", "added@example.com"}})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var invited []string
	rows, err := testPool.Query(ctx, `SELECT email FROM event_invitations`)
	require.NoError(t, err)
	for rows.Next() {
		var e string
		require.NoError(t, rows.Scan(&e))
		invited = append(invited, e)
	}
	rows.Close()
	assert.Equal(t, []string{"added@example.com"}, invited)
}

func shareCalendar(t *testing.T, ownerToken string, calID uuid.UUID, email, permission string) uuid.UUID {
	t.Helper()
	w := Do(t, testRouter, "POST", fmt.Sprintf("/calendars/%s/shares", calID), ownerToken, map[string]string{
		"email": email, "permission": permission,
	})
	require.Less(t, w.Code, 300, w.Body.String())
	return decodeID(t, w)
}

func defaultCalendar(t *testing.T, token string) uuid.UUID {
	t.Helper()
	w := Do(t, testRouter, "GET", "/calendars", token, nil)
	var cals []struct {
		ID        uuid.UUID `json:"id"`
		IsDefault bool      `json:"is_default"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&cals))
	for _, c := range cals {
		if c.IsDefault {
			return c.ID
		}
	}
	t.Fatal("no default calendar")
	return uuid.Nil
}

func TestEventHandler_SharedCalendarWriteAccess(t *testing.T) {
	truncateAll(t, testPool)
	_, alice := MustRegisterAndLogin(t, testRouter, "share_alice")
	_, bob := MustRegisterAndLogin(t, testRouter, "share_bob")
	cal := defaultCalendar(t, alice)
	shareID := shareCalendar(t, alice, cal, "user_share_bob@example.com", "edit")

	create := func(token, title string) uuid.UUID {
		w := Do(t, testRouter, "POST", "/events", token, futureEvent(map[string]interface{}{"calendar_id": cal, "title": title}))
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		return decodeID(t, w)
	}
	put := func(token string, id uuid.UUID) int {
		return Do(t, testRouter, "PUT", "/events/"+id.String(), token, map[string]string{"title": "edited"}).Code
	}
	bobs, bobs2, alices := create(bob, "Bob's"), create(bob, "Bob's 2"), create(alice, "Alice's")

	// Bob files his event under his own Area. Alice can still edit it (the UI sends the
	// unchanged category back), but can't file it under one of hers.
	bobsArea, alicesArea := createCategory(t, bob, "Bob's area", 0), createCategory(t, alice, "Alice's area", 0)
	require.Equal(t, http.StatusOK, Do(t, testRouter, "PUT", "/events/"+bobs2.String(), bob, map[string]interface{}{"category_id": bobsArea}).Code)
	w := Do(t, testRouter, "PUT", "/events/"+bobs2.String(), alice, map[string]interface{}{"title": "typo fixed", "category_id": bobsArea})
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, http.StatusBadRequest, Do(t, testRouter, "PUT", "/events/"+bobs2.String(), alice, map[string]interface{}{"category_id": alicesArea}).Code)

	// The calendar owner can edit and delete what a collaborator added.
	assert.Equal(t, http.StatusOK, put(alice, bobs))
	assert.Equal(t, http.StatusNoContent, Do(t, testRouter, "DELETE", "/events/"+bobs.String(), alice, nil).Code)
	// An editor can edit their own events, but not the owner's.
	assert.Equal(t, http.StatusOK, put(bob, bobs2))
	assert.Equal(t, http.StatusNotFound, put(bob, alices))
	assert.Equal(t, http.StatusNotFound, Do(t, testRouter, "DELETE", "/events/"+alices.String(), bob, nil).Code)

	// Downgrading to view-only, then revoking, takes the editor's write access away —
	// even to events they added.
	shareCalendar(t, alice, cal, "user_share_bob@example.com", "view")
	assert.Equal(t, http.StatusNotFound, put(bob, bobs2), "view-only")
	require.Less(t, Do(t, testRouter, "DELETE", fmt.Sprintf("/calendars/%s/shares/%s", cal, shareID), alice, nil).Code, 300)
	assert.Equal(t, http.StatusNotFound, put(bob, bobs2), "revoked")
	assert.Equal(t, http.StatusNotFound, Do(t, testRouter, "DELETE", "/events/"+bobs2.String(), bob, nil).Code)
	assert.Equal(t, http.StatusOK, put(alice, bobs2), "still the owner's to manage")
}

func TestUserHandler_CredentialChangesNeedCurrentPasswordAndEndSessions(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "creds")
	// A second session, e.g. another device (or a stolen token).
	w := Do(t, testRouter, "POST", "/auth/login", "", map[string]string{"email": "user_creds@example.com", "password": "password123"})
	require.Equal(t, http.StatusOK, w.Code)
	var other struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&other))

	change := func(body map[string]string) *httptest.ResponseRecorder {
		return Do(t, testRouter, "PUT", "/users/me", token, body)
	}
	assert.Equal(t, http.StatusBadRequest, change(map[string]string{"email": "new@example.com"}).Code, "no current password")
	assert.Equal(t, http.StatusForbidden, change(map[string]string{"password": "brand-new-pass", "current_password": "wrong"}).Code)
	assert.Equal(t, http.StatusOK, change(map[string]string{"username": "renamed"}).Code, "a username change needs none")

	// A password change signs out every session issued before it — even one issued a
	// moment earlier, in the same second — and returns a fresh token.
	w = Do(t, testRouter, "POST", "/auth/login", "", map[string]string{"email": "user_creds@example.com", "password": "password123"})
	require.Equal(t, http.StatusOK, w.Code)
	var justBefore struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&justBefore))
	w = change(map[string]string{"password": "brand-new-pass", "current_password": "password123"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Username string `json:"username"`
		Token    string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "renamed", resp.Username)
	require.NotEmpty(t, resp.Token)
	assert.Equal(t, http.StatusUnauthorized, Do(t, testRouter, "GET", "/users/me", other.Token, nil).Code, "the other session is signed out")
	assert.Equal(t, http.StatusUnauthorized, Do(t, testRouter, "GET", "/users/me", justBefore.Token, nil).Code, "no same-second window")
	assert.Equal(t, http.StatusUnauthorized, Do(t, testRouter, "GET", "/users/me", token, nil).Code, "so is the one that changed it")
	assert.Equal(t, http.StatusOK, Do(t, testRouter, "GET", "/users/me", resp.Token, nil).Code, "the returned token works")

	// A deleted account's tokens stop working at once.
	require.Equal(t, http.StatusNoContent, Do(t, testRouter, "DELETE", "/users/me", resp.Token, nil).Code)
	assert.Equal(t, http.StatusUnauthorized, Do(t, testRouter, "GET", "/users/me", resp.Token, nil).Code)
}

func TestAuthHandler_EmailsAreCaseInsensitive(t *testing.T) {
	truncateAll(t, testPool)
	w := Do(t, testRouter, "POST", "/auth/register", "", map[string]string{
		"username": "bob", "email": "Bob@Example.com", "password": "password123",
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var reg struct {
		Email string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&reg))
	assert.Equal(t, "bob@example.com", reg.Email, "stored lowercased")

	w = Do(t, testRouter, "POST", "/auth/register", "", map[string]string{
		"username": "mallory", "email": "BOB@example.COM", "password": "password123",
	})
	assert.Equal(t, http.StatusConflict, w.Code, "a case variant is the same address")

	w = Do(t, testRouter, "POST", "/auth/login", "", map[string]string{"email": "BOB@EXAMPLE.COM", "password": "password123"})
	require.Equal(t, http.StatusOK, w.Code, "login ignores case")

	_, alice := MustRegisterAndLogin(t, testRouter, "ci_alice")
	shareCalendar(t, alice, defaultCalendar(t, alice), "BoB@example.com", "view")

	// A database from before emails were case-insensitive can hold a case variant of an
	// existing address (Migrate then can't create the unique index). Lookups resolve to
	// the exact, lowercased address first — the original account — not the look-alike.
	ctx := context.Background()
	_, err := testPool.Exec(ctx, `DROP INDEX users_email_lower_uniq`)
	require.NoError(t, err)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM users WHERE username = 'squatter'`)
		testPool.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_uniq ON users (lower(email))`)
	})
	_, err = testPool.Exec(ctx, `INSERT INTO users (username, email, password_hash) VALUES ('squatter', 'Bob@Example.COM', 'x')`)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		w = Do(t, testRouter, "POST", "/auth/login", "", map[string]string{"email": "BOB@example.com", "password": "password123"})
		require.Equal(t, http.StatusOK, w.Code, "still Bob's account: %s", w.Body.String())
	}
	w = Do(t, testRouter, "POST", fmt.Sprintf("/calendars/%s/shares", defaultCalendar(t, alice)), alice, map[string]string{
		"email": "bob@EXAMPLE.com", "permission": "edit",
	})
	require.Less(t, w.Code, 300, w.Body.String())
	assert.Contains(t, w.Body.String(), `"bob"`, "shared with Bob, not the squatter")
}

func TestRecurringEvents_PrivateSeries(t *testing.T) {
	truncateAll(t, testPool)
	_, owner := MustRegisterAndLogin(t, testRouter, "priv_owner")
	_, viewer := MustRegisterAndLogin(t, testRouter, "priv_viewer")
	cal := defaultCalendar(t, owner)
	shareCalendar(t, owner, cal, "user_priv_viewer@example.com", "view")

	w := Do(t, testRouter, "POST", "/recurring-events", owner, futureEvent(map[string]interface{}{
		"title": "Therapy", "frequency": "weekly", "max_occurrences": 3, "visibility": "Private",
	}))
	assert.Equal(t, http.StatusBadRequest, w.Code, "visibility values are validated")
	w = Do(t, testRouter, "POST", "/recurring-events", owner, futureEvent(map[string]interface{}{
		"title": "Therapy", "frequency": "weekly", "max_occurrences": 3, "visibility": "private",
	}))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	titles := func(token string) []string {
		w := Do(t, testRouter, "GET", "/events?calendar_id="+cal.String(), token, nil)
		var evs []struct {
			ID    uuid.UUID `json:"id"`
			Title string    `json:"title"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&evs))
		var out []string
		for _, e := range evs {
			out = append(out, e.Title)
		}
		return out
	}
	assert.Equal(t, []string{"Busy", "Busy", "Busy"}, titles(viewer))
	assert.Equal(t, []string{"Therapy", "Therapy", "Therapy"}, titles(owner))

	// Making the whole series public again reaches every occurrence.
	w = Do(t, testRouter, "GET", "/events?calendar_id="+cal.String(), owner, nil)
	var evs []struct {
		ID        uuid.UUID `json:"id"`
		StartTime string    `json:"start_time"`
		EndTime   string    `json:"end_time"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&evs))
	w = Do(t, testRouter, "PUT", "/events/"+evs[1].ID.String()+"/recurrence", owner, map[string]interface{}{
		"scope": "all", "visibility": "public", "start_time": evs[1].StartTime, "end_time": evs[1].EndTime,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, []string{"Therapy", "Therapy", "Therapy"}, titles(viewer))

	// Drag the last occurrence (it becomes an exception of the series), then make the
	// series private again: the exception is hidden along with the rest.
	w = Do(t, testRouter, "GET", "/events?calendar_id="+cal.String(), owner, nil)
	require.NoError(t, json.NewDecoder(w.Body).Decode(&evs))
	last := evs[2]
	start, _ := time.Parse(time.RFC3339, last.StartTime)
	w = Do(t, testRouter, "PUT", "/events/"+last.ID.String(), owner, map[string]string{
		"start_time": start.Add(30 * time.Minute).Format(time.RFC3339), "end_time": start.Add(90 * time.Minute).Format(time.RFC3339),
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = Do(t, testRouter, "PUT", "/events/"+evs[0].ID.String()+"/recurrence", owner, map[string]interface{}{
		"scope": "all", "visibility": "private", "start_time": evs[0].StartTime, "end_time": evs[0].EndTime,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, []string{"Busy", "Busy", "Busy"}, titles(viewer), "including the dragged occurrence")
}
