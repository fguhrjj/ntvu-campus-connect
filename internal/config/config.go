// Package config stores settings with the password protected by DPAPI (spec §3).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SchemaVersion is the only supported config schema.
const SchemaVersion = 1

// ErrUnsupported is returned by the protector on non-Windows builds.
var ErrUnsupported = errors.New("config: DPAPI is only available on Windows")

// Config mirrors %APPDATA%\CampusConnect\config.json.
type Config struct {
	SchemaVersion     int    `json:"schemaVersion"`
	Username          string `json:"username"`
	PasswordProtected string `json:"passwordProtected"`
	ServiceValue      string `json:"serviceValue"`
	ServiceLabel      string `json:"serviceLabel"`
	SSID              string `json:"ssid"`
	Autostart         bool   `json:"autostart"`
}

// Protector encrypts secrets for the current user.
type Protector interface {
	Protect([]byte) (string, error)
	Unprotect(string) ([]byte, error)
}

// Default is the unconfigured state: China Unicom on NTVU-R.
func Default() Config {
	return Config{SchemaVersion: SchemaVersion, ServiceValue: "CUCC", ServiceLabel: "中国联通", SSID: "NTVU-R"}
}

// Configured reports whether login credentials are present.
func (c Config) Configured() bool {
	return c.Username != "" && c.PasswordProtected != "" && c.ServiceValue != ""
}

// Password decrypts the stored password. Call it only right before submitting.
func (c Config) Password(p Protector) (string, error) {
	if c.PasswordProtected == "" {
		return "", errors.New("config: no password saved")
	}
	b, err := p.Unprotect(c.PasswordProtected)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Load reads path. A missing file yields Default() and no error.
func Load(path string, _ Protector) (Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("config: %s is corrupt: %w", path, err)
	}
	if c.SchemaVersion != SchemaVersion {
		return Config{}, fmt.Errorf("config: unsupported schema version %d", c.SchemaVersion)
	}
	if c.SSID == "" {
		c.SSID = Default().SSID
	}
	return c, nil
}

// Save writes cfg atomically. A non-empty newPassword is protected and
// stored; an empty one keeps the ciphertext already on disk.
func Save(path string, cfg Config, newPassword string, p Protector) error {
	cfg.SchemaVersion = SchemaVersion
	if newPassword != "" {
		ct, err := p.Protect([]byte(newPassword))
		if err != nil {
			return err
		}
		cfg.PasswordProtected = ct
	} else if old, err := Load(path, p); err == nil && old.PasswordProtected != "" {
		cfg.PasswordProtected = old.PasswordProtected
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
