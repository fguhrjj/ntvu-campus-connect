package ui

import (
	"context"
	"errors"
	"strings"

	"campusconnect/internal/config"
	"campusconnect/internal/portal"
)

// Store loads and saves settings for the UI. Save receives the plaintext
// password only when the user typed a new one ("" keeps the saved one) and
// applies cfg.Autostart; Services fetches the operator list from the portal.
type Store interface {
	Load() (config.Config, error)
	Save(cfg config.Config, newPassword string) error
	Services(ctx context.Context) ([]portal.Service, error)
}

// formField names the settings control a validation error points at.
type formField int

const (
	fieldNone formField = iota
	fieldUsername
	fieldPassword
	fieldService
)

// settingsInput is what the user entered in the settings window.
type settingsInput struct {
	Username  string
	Password  string // "" keeps the saved password
	Service   int    // index into the offered services, -1 when none
	Autostart bool
}

var (
	errNoUsername = errors.New("请填写学号")
	errNoPassword = errors.New("请填写密码")
	errNoService  = errors.New("请选择运营商")
)

// applySettings validates in against the offered services and returns the
// config to save. The password itself is not part of the result: the store
// protects in.Password when non-empty and otherwise keeps the saved one, so
// a blank password is valid only when one is already saved.
func applySettings(saved config.Config, in settingsInput, services []portal.Service) (config.Config, formField, error) {
	username := strings.TrimSpace(in.Username)
	switch {
	case username == "":
		return saved, fieldUsername, errNoUsername
	case in.Password == "" && saved.PasswordProtected == "":
		return saved, fieldPassword, errNoPassword
	case in.Service < 0 || in.Service >= len(services):
		return saved, fieldService, errNoService
	}
	cfg := saved
	cfg.Username = username
	cfg.ServiceValue = services[in.Service].Value
	cfg.ServiceLabel = services[in.Service].Label
	cfg.Autostart = in.Autostart
	return cfg, fieldNone, nil
}

// pickService selects current in list, else China Unicom (the spec
// default), else nothing (-1).
func pickService(list []portal.Service, current string) int {
	unicom := -1
	for i, s := range list {
		if s.Value == current {
			return i
		}
		if s.Value == config.Default().ServiceValue && unicom < 0 {
			unicom = i
		}
	}
	return unicom
}

// savedServices is the offer while the portal list is unavailable: only
// the operator already in the config (spec §7).
func savedServices(cfg config.Config) []portal.Service {
	if cfg.ServiceValue == "" {
		return nil
	}
	label := cfg.ServiceLabel
	if label == "" {
		label = cfg.ServiceValue
	}
	return []portal.Service{{Label: label, Value: cfg.ServiceValue}}
}
