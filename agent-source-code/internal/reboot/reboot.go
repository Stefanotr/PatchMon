// Package reboot schedules and cancels host restarts for patch runs and for
// the server's restart command.
//
// The server only ever sends a mode, a delay or a time, and a message. All
// three are bounded here, and the restart itself goes through the operating
// system's own scheduler (InitiateSystemShutdownEx on Windows, shutdown(8)
// elsewhere) with an argument vector, never a shell. The OS scheduler warns
// signed-in users and survives the agent being restarted.
package reboot

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Mode says whether a patch run restarts the host when it is over.
type Mode string

const (
	// ModeNever leaves restarting to the operator (the default).
	ModeNever Mode = "never"
	// ModeIfNeeded restarts when the host reports a pending restart.
	ModeIfNeeded Mode = "if_needed"
	// ModeAlways restarts after every real run.
	ModeAlways Mode = "always"
)

const (
	// MinDelay leaves the run time to send its final status and the
	// post-patch report (each given up to two minutes) before the host goes
	// down, and signed-in users time to save their work.
	MinDelay = 5 * time.Minute
	// MaxDelay bounds how far ahead a restart can be scheduled.
	MaxDelay = 7 * 24 * time.Hour
	// MaxMessageBytes bounds the message shown to signed-in users.
	MaxMessageBytes = 200

	messagePrefix  = "PatchMon: "
	defaultMessage = "this computer restarts to finish installing updates"
)

// ErrNothingScheduled is returned by Cancel when no restart was pending.
var ErrNothingScheduled = errors.New("no restart is scheduled")

// ErrAlreadyScheduled is returned by Schedule when the operating system
// already has a restart or shutdown pending, whoever asked for it.
var ErrAlreadyScheduled = errors.New("a restart or shutdown is already pending on this host")

// Options is the restart part of a patch run or of a restart command, as the
// server sends it.
type Options struct {
	Mode Mode `json:"mode"`
	// DelayMinutes after the run, or after the command arrives.
	DelayMinutes int `json:"delay_minutes"`
	// At is an absolute time; it wins over DelayMinutes when set.
	At      time.Time `json:"at"`
	Message string    `json:"message"`
}

// Validate checks options received from the server.
func (o Options) Validate() error {
	switch o.Mode {
	case "", ModeNever, ModeIfNeeded, ModeAlways:
	default:
		return fmt.Errorf("unknown restart mode %q", o.Mode)
	}
	if o.DelayMinutes < 0 || time.Duration(o.DelayMinutes)*time.Minute > MaxDelay {
		return fmt.Errorf("restart delay out of range: %d minutes", o.DelayMinutes)
	}
	if len(o.Message) > MaxMessageBytes || !utf8.ValidString(o.Message) {
		return errors.New("restart message is too long or not valid UTF-8")
	}
	return nil
}

// Enabled reports whether the options ask for a restart at all.
func (o Options) Enabled() bool {
	return o.Mode == ModeIfNeeded || o.Mode == ModeAlways
}

// Wanted reports whether a run ending with needed restarts the host.
func (o Options) Wanted(needed bool) bool {
	return o.Mode == ModeAlways || (o.Mode == ModeIfNeeded && needed)
}

// When returns the time the restart happens, at least MinDelay from now.
//
// A delay counts from now. An absolute time that has passed moves on by
// whole days: "at 03:00" picked for a run that a policy delayed, or that
// waited for its host to come online, waits for the next 03:00 instead of
// restarting the host right after the run, in the middle of the day. A time
// still ahead but closer than MinDelay is pushed back to MinDelay. A time
// more than MaxDelay away is refused rather than brought forward.
func (o Options) When(now time.Time) (time.Time, error) {
	earliest := now.Add(MinDelay)
	if o.At.IsZero() {
		d := min(max(time.Duration(o.DelayMinutes)*time.Minute, MinDelay), MaxDelay)
		return now.Add(d), nil
	}
	at := o.At
	if at.Before(now) {
		days := int(earliest.Sub(at)/(24*time.Hour)) + 1
		at = at.AddDate(0, 0, days)
	}
	if at.Before(earliest) {
		at = earliest
	}
	if at.After(now.Add(MaxDelay)) {
		return time.Time{}, fmt.Errorf("the requested time %s is more than %s away", at.UTC().Format(time.RFC3339), MaxDelay)
	}
	return at, nil
}

// ShutdownMessage is the text shown to signed-in users. It is one printable
// line and always starts with the prefix, so it can never be read as an
// option by shutdown(8).
func (o Options) ShutdownMessage() string {
	msg := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			return ' '
		}
		return r
	}, o.Message))
	if msg == "" {
		msg = defaultMessage
	}
	msg = messagePrefix + msg
	if len(msg) > MaxMessageBytes+len(messagePrefix) {
		msg = msg[:MaxMessageBytes+len(messagePrefix)]
		for !utf8.ValidString(msg) {
			msg = msg[:len(msg)-1]
		}
	}
	return msg
}
