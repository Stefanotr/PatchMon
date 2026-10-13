package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"

	"patchmon-agent/internal/reboot"
)

// The restart this agent scheduled is recorded in a state file, so that a
// cancel only ever touches that restart and the record survives the agent
// restarting (self-update, apply_config). Only the agent writes the file,
// readable by root or SYSTEM alone, and every read checks it again: a file
// that cannot be trusted, or does not hold a valid record, is dropped.

const (
	restartRecordFile = "restart-scheduled.json"
	// restartRecordMaxBytes bounds what is read: a record is a few dozen.
	restartRecordMaxBytes = 1024
	restartRecordVersion  = 1
)

// restartRecordPath is where the record lives, a variable for tests.
var restartRecordPath = defaultRestartRecordPath

type restartRecordJSON struct {
	Version     int       `json:"version"`
	At          time.Time `json:"at"`
	ScheduledAt time.Time `json:"scheduled_at"`
}

func encodeRestartRecord(s reboot.Scheduled) ([]byte, error) {
	return json.Marshal(restartRecordJSON{Version: restartRecordVersion, At: s.At.UTC(), ScheduledAt: s.Since.UTC()})
}

// decodeRestartRecord reads a record and checks it holds together: known
// version and fields only, a restart time after the time it was scheduled
// and no further than reboot.MaxDelay.
func decodeRestartRecord(r io.Reader) (reboot.Scheduled, error) {
	b, err := io.ReadAll(io.LimitReader(r, restartRecordMaxBytes+1))
	if err != nil {
		return reboot.Scheduled{}, err
	}
	if len(b) > restartRecordMaxBytes {
		return reboot.Scheduled{}, errors.New("the record is too large")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var v restartRecordJSON
	if err := dec.Decode(&v); err != nil {
		return reboot.Scheduled{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return reboot.Scheduled{}, errors.New("unexpected data after the record")
	}
	switch {
	case v.Version != restartRecordVersion:
		return reboot.Scheduled{}, fmt.Errorf("unknown record version %d", v.Version)
	case v.At.IsZero() || v.ScheduledAt.IsZero():
		return reboot.Scheduled{}, errors.New("the record lacks a time")
	case v.At.Before(v.ScheduledAt) || v.At.Sub(v.ScheduledAt) > reboot.MaxDelay+time.Minute:
		return reboot.Scheduled{}, errors.New("the record's times do not hold together")
	}
	return reboot.Scheduled{At: v.At, Since: v.ScheduledAt}, nil
}

func readRestartRecord(path string) (reboot.Scheduled, error) {
	f, err := openRestartRecord(path)
	if err != nil {
		return reboot.Scheduled{}, err
	}
	defer func() { _ = f.Close() }()
	s, err := decodeRestartRecord(f)
	if err != nil {
		return reboot.Scheduled{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// restartRecordLocked returns the restart this agent recorded, reading the
// file the first time. The caller holds restartGate.
func restartRecordLocked() (reboot.Scheduled, bool) {
	if !restartGate.recordLoaded {
		restartGate.recordLoaded = true
		s, err := readRestartRecord(restartRecordPath())
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			logger.WithError(err).Warn("Dropping a scheduled restart record that cannot be trusted")
			removeRestartRecord()
		default:
			restartGate.record = &s
		}
	}
	if restartGate.record == nil {
		return reboot.Scheduled{}, false
	}
	return *restartGate.record, true
}

// setRestartRecordLocked records a restart this agent scheduled. Should the
// file not be written, the agent still knows of the restart until it is
// restarted itself. The caller holds restartGate.
func setRestartRecordLocked(s reboot.Scheduled) {
	restartGate.record, restartGate.recordLoaded = &s, true
	b, err := encodeRestartRecord(s)
	if err == nil {
		err = writeRestartRecordFile(restartRecordPath(), b)
	}
	if err != nil {
		logger.WithError(err).Warn("Could not record the scheduled restart on disk; it is known until the agent restarts")
	}
}

// clearRestartRecordLocked forgets the restart this agent scheduled. The
// caller holds restartGate.
func clearRestartRecordLocked(why string) {
	restartGate.record, restartGate.recordLoaded = nil, true
	logger.WithField("reason", why).Info("Forgetting the restart PatchMon scheduled")
	removeRestartRecord()
}

func removeRestartRecord() {
	if err := removeRestartRecordFile(restartRecordPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logger.WithError(err).Warn("Could not remove the scheduled restart record")
	}
}
