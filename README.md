# Calendar

A Google Calendar-style backend built with Go, PostgreSQL, and Redis.

## Architecture

```
┌─────────┐    ┌──────────────────────────────────────────┐
│  nginx  │───▶│                  api                     │
│ :80     │    │  Gin · pgx/v5 · JWT · Redis · :8080      │
└─────────┘    └──────────────────────────────────────────┘
                        │                  │
               ┌────────┘          ┌───────┘
               ▼                   ▼
          ┌──────────┐       ┌──────────┐
          │ postgres │       │  redis   │
          │  :5432   │       │  :6379   │
          └──────────┘       └──────────┘
               ▲                   ▲
               │                   │
    ┌──────────────────┐   ┌──────────────────┐
    │    scheduler     │   │  notification    │
    │  (hourly cron)   │   │  (poll every 30s)│
    └──────────────────┘   └──────────────────┘
```

Three microservices:

| Service | Role |
|---|---|
| **api** | REST API — all user-facing endpoints |
| **scheduler** | Extends recurring event instance windows 60 days out |
| **notification** | Sends reminder emails and invitation emails via SMTP |

Email is captured by [MailHog](https://github.com/mailhog/mailhog) in development (`localhost:8025`).

## Features

- **Auth** — register / login with JWT
- **Calendars** — multiple calendars per user, ICS import/export
- **Events** — full CRUD, all-day events, timezone support, visibility (`public` / `private`)
- **Recurring events** — daily / weekly / monthly / yearly with per-instance editing (this / this-and-following / all)
- **Reminders** — multiple reminders per event with `email` or `notification` method
- **Search** — full-text search over title, description, location, and attendees
- **Calendar sharing** — share with `view` or `edit` permission; shared events respect visibility
- **Free/busy** — query availability across multiple users
- **Invitations** — tokenized email links; attendees respond accept / decline / tentative
- **RSVP statuses** — per-attendee `accepted` / `declined` / `tentative` / `needs_action` on `GET /events/:id`
- **Categories** — color-coded event categories

## Quickstart

```bash
docker compose up
```

API is available at `http://localhost:8080` (direct) or `http://localhost/api/` (via nginx).  
MailHog UI is at `http://localhost:8025`.

## API Overview

### Auth
```
POST /auth/register
POST /auth/login
```

### Users
```
GET    /users/me
PUT    /users/me
DELETE /users/me
```

### Calendars
```
POST   /calendars
GET    /calendars
GET    /calendars/:id
PUT    /calendars/:id
DELETE /calendars/:id

GET    /calendars/shared-with-me
POST   /calendars/:id/shares
GET    /calendars/:id/shares
DELETE /calendars/:id/shares/:share_id

GET    /calendars/:id/export        # ICS download
POST   /calendars/:id/import        # ICS upload
```

### Events
```
POST   /events
GET    /events[?calendar_id=&from=&to=]
GET    /events/search?q=
GET    /events/:id                  # includes attendee_statuses
PUT    /events/:id
DELETE /events/:id

PUT    /events/:id/recurrence       # scope: this | this_and_following | all
```

### Recurring Events
```
POST   /recurring-events
GET    /recurring-events
GET    /recurring-events/:id
PUT    /recurring-events/:id
DELETE /recurring-events/:id
```

### Free/Busy
```
GET /free-busy?emails=a@x.com,b@x.com&from=<RFC3339>&to=<RFC3339>
```

### Categories
```
POST   /categories
GET    /categories
GET    /categories/:id
PUT    /categories/:id
DELETE /categories/:id
```

### Invitations (no auth required)
```
GET /invitations/:token/accept
GET /invitations/:token/decline
GET /invitations/:token/tentative
```

## Environment Variables

All services read configuration from environment variables. Defaults work out of the box with `docker compose`.

| Variable | Service | Default | Description |
|---|---|---|---|
| `DB_HOST` | api, scheduler, notification | `localhost` | PostgreSQL host |
| `DB_PORT` | api, scheduler, notification | `5432` | PostgreSQL port |
| `DB_USER` | api, scheduler, notification | `calendar` | PostgreSQL user |
| `DB_PASSWORD` | api, scheduler, notification | `calendar` | PostgreSQL password |
| `DB_NAME` | api, scheduler, notification | `calendar` | PostgreSQL database |
| `REDIS_ADDR` | api, notification | `localhost:6379` | Redis address |
| `JWT_SECRET` | api | `secret` | JWT signing key — **change in production** |
| `PORT` | api | `8080` | HTTP listen port |
| `SMTP_HOST` | notification | `localhost` | SMTP host |
| `SMTP_PORT` | notification | `1025` | SMTP port |
| `BASE_URL` | notification | `http://localhost:8080` | Used in invitation email links |
