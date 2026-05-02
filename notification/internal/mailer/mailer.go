package mailer

import (
	"fmt"
	"net/smtp"
	"os"
	"strings"
	"time"
)

type Mailer struct {
	host string
	port string
	from string
}

func New() *Mailer {
	return &Mailer{
		host: getEnv("SMTP_HOST", "localhost"),
		port: getEnv("SMTP_PORT", "1025"),
		from: getEnv("SMTP_FROM", "calendar@example.com"),
	}
}

func (m *Mailer) SendInvitation(to, title, location string, startTime time.Time, acceptURL, declineURL string) error {
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: Invitation: %s\r\n\r\n"+
			"You are invited to \"%s\"\nWhen: %s\nWhere: %s\n\nAccept:  %s\nDecline: %s",
		m.from, to, title,
		title, startTime.Format(time.RFC1123), location,
		acceptURL, declineURL,
	)
	return smtp.SendMail(m.host+":"+m.port, nil, m.from, []string{to}, []byte(msg))
}

func (m *Mailer) SendReminder(title string, startTime time.Time, attendees []string) error {
	if len(attendees) == 0 {
		return nil
	}
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: Reminder: %s\r\n\r\nYour event \"%s\" starts at %s.",
		m.from,
		strings.Join(attendees, ", "),
		title,
		title,
		startTime.Format(time.RFC1123),
	)
	return smtp.SendMail(m.host+":"+m.port, nil, m.from, attendees, []byte(msg))
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
