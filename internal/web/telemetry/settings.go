package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
)

type preferences struct {
	Enabled        *bool  `json:"enabled,omitempty"`
	NoticeSeen     bool   `json:"notice_seen"`
	InstallationID string `json:"installation_id,omitempty"`
	Generation     string `json:"generation"`
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "renart", "telemetry.json"), nil
}

func readPreferences(path string) (preferences, error) {
	var p preferences
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	// Refuse pipes and devices before opening them: a telemetry setting must
	// never wait for another process to supply bytes.
	if !info.Mode().IsRegular() {
		return p, errors.New("usage settings must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return p, err
	}
	if len(data) > 8192 {
		return p, errors.New("usage settings too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&p); err != nil {
		return preferences{}, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return preferences{}, errors.New("invalid usage settings")
	}
	if p.InstallationID != "" {
		id, parseErr := uuid.Parse(p.InstallationID)
		if parseErr != nil || id.Version() != 4 {
			return preferences{}, errors.New("invalid installation ID")
		}
	}
	return p, nil
}

// Cross-process locking prevents simultaneous launches creating different IDs
// and prevents an ID write from overwriting an opt-out from another process.
func changePreferences(path string, mutate func(*preferences)) (preferences, error) {
	if path == "" {
		return preferences{}, errors.New("usage settings location unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return preferences{}, err
	}
	lock := flock.New(path + ".lock")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ok, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return preferences{}, err
	}
	if !ok {
		return preferences{}, errors.New("usage settings are busy")
	}
	defer lock.Unlock()
	p, err := readPreferences(path)
	if err != nil {
		return preferences{}, err
	}
	mutate(&p)
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return preferences{}, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".telemetry-*")
	if err != nil {
		return preferences{}, err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = io.Copy(file, bytes.NewReader(append(data, '\n')))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return preferences{}, err
	}
	if closeErr != nil {
		return preferences{}, closeErr
	}
	if err = os.Rename(name, path); err != nil {
		return preferences{}, err
	}
	return p, nil
}
