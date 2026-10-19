package auth

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type Service struct {
	repo    Repository
	secrets Secrets
	dummy   string
}

func New(repo Repository, pepper string) *Service {
	s := &Service{repo: repo, secrets: Secrets{pepper}}
	s.dummy = s.secrets.Hash(Token(), false)
	return s
}

func (s *Service) identity(tx Tx, r Request, mutation bool) (Identity, error) {
	if !validToken(r.SessionToken) {
		return Identity{}, Fail("AUTH_REQUIRED", 401)
	}
	session, err := tx.Session(Digest(r.SessionToken))
	if errors.Is(err, ErrMissing) {
		return Identity{}, Fail("AUTH_REQUIRED", 401)
	}
	if err != nil {
		return Identity{}, err
	}
	now := time.Now()
	if session.Revoked || !session.Idle.After(now) || !session.Absolute.After(now) {
		return Identity{}, Fail("AUTH_REQUIRED", 401)
	}
	u, err := tx.User(session.UserID)
	if err != nil {
		return Identity{}, err
	}
	if !u.Active || u.AuthVersion != session.AuthVersion {
		return Identity{}, Fail("AUTH_REQUIRED", 401)
	}
	trusted := false
	if session.DeviceID != "" {
		d, err := tx.Device(Digest(r.DeviceToken))
		if err != nil && !errors.Is(err, ErrMissing) {
			return Identity{}, err
		}
		if err != nil || d.ID != session.DeviceID || d.Revoked || !d.Expires.After(now) {
			return Identity{}, Fail("DEVICE_REQUIRED", 403)
		}
		trusted = true
	} else if validToken(r.DeviceToken) {
		d, e := tx.Device(Digest(r.DeviceToken))
		trusted = e == nil && !d.Revoked && d.Expires.After(now)
		if e != nil && !errors.Is(e, ErrMissing) {
			return Identity{}, e
		}
	}
	if mutation && (r.ActorID != u.ID || !validToken(r.CSRF) || !Equal(session.CSRFHash, Digest(r.CSRF))) {
		return Identity{}, Fail("SESSION_CHANGED", 403)
	}
	return Identity{User: u, DeviceTrusted: trusted, PINSession: session.DeviceID != "", Session: session}, nil
}
func (s *Service) authenticated(ctx context.Context, r Request, role string, mutation bool, fn func(Tx, Identity) error) error {
	var actor Identity
	err := s.repo.Within(ctx, func(tx Tx) error {
		id, err := s.identity(tx, r, mutation)
		if err != nil {
			return err
		}
		actor = id
		if role != "" && !id.User.Has(role) {
			return Fail("FORBIDDEN", 403)
		}
		return fn(tx, id)
	})
	var failure *Failure
	if errors.As(err, &failure) || errors.Is(err, ErrMissing) {
		reason := "NOT_FOUND"
		if failure != nil {
			reason = failure.Code
		}
		// Business transaction is already rolled back; rejected requests have their
		// own committed audit without caller-supplied actor IDs or secret payloads.
		if auditErr := s.repo.Within(ctx, func(tx Tx) error { return audit(tx, r, actor, "request_rejected", "", false, reason) }); auditErr != nil {
			return auditErr
		}
	}
	return err
}

// Authorize rechecks identity within the caller's transaction; it does not open
// another transaction or grant object ownership. Consumers enforce object rules.
func (s *Service) Authorize(tx Tx, r Request, role string, mutation bool) (Identity, error) {
	id, err := s.identity(tx, r, mutation)
	if err != nil {
		return Identity{}, err
	}
	if role != "" && !id.User.Has(role) {
		return Identity{}, Fail("FORBIDDEN", 403)
	}
	return id, nil
}
func audit(tx Tx, r Request, id Identity, action, entity string, success bool, reason string) error {
	return tx.Audit(Event{ActorID: id.User.ID, DeviceID: id.Session.DeviceID, Action: action, EntityID: entity, RequestID: r.RequestID, Reason: reason, Success: success})
}

func (s *Service) Me(ctx context.Context, r Request) (Identity, error) {
	var result Identity
	err := s.authenticated(ctx, r, "", false, func(_ Tx, id Identity) error { result = id; return nil })
	return result, err
}
func (s *Service) AdminOptions(ctx context.Context, r Request) ([]User, error) {
	result := []User{}
	err := s.repo.Within(ctx, func(tx Tx) error {
		d, err := tx.Device(Digest(r.DeviceToken))
		if errors.Is(err, ErrMissing) {
			return Fail("DEVICE_REQUIRED", 403)
		}
		if err != nil {
			return err
		}
		if d.Revoked || !d.Expires.After(time.Now()) {
			return Fail("DEVICE_REQUIRED", 403)
		}
		users, err := tx.Users()
		if err != nil {
			return err
		}
		for _, u := range users {
			if u.Active && u.Has("admin") {
				result = append(result, User{ID: u.ID, Name: u.Name, Roles: []string{"admin"}, Active: true})
			}
		}
		return nil
	})
	return result, err
}

func (s *Service) rateKeys(source, subject string) []string {
	return []string{"source:" + Digest(source), "subject:" + Digest(subject)}
}
func blocked(tx Tx, keys []string) (bool, error) {
	for _, k := range keys {
		a, err := tx.Attempt(k)
		if err != nil && !errors.Is(err, ErrMissing) {
			return false, err
		}
		if err == nil && a.Blocked.After(time.Now()) {
			return true, nil
		}
	}
	return false, nil
}
func failed(tx Tx, keys []string) error {
	now := time.Now()
	for _, k := range keys {
		a, err := tx.Attempt(k)
		if err != nil && !errors.Is(err, ErrMissing) {
			return err
		}
		if errors.Is(err, ErrMissing) || !a.Start.Add(5*time.Minute).After(now) {
			a = Attempt{Key: k, Start: now, Blocked: now}
		}
		a.Count++
		if a.Count >= 5 {
			a.Blocked = now.Add(5 * time.Minute)
		}
		if err := tx.SaveAttempt(a); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) Login(ctx context.Context, r Request, subject, secret string, pin bool) (LoginResult, error) {
	var result LoginResult
	var denied error
	err := s.repo.Within(ctx, func(tx Tx) error {
		// Revoke the browser's old personal session even if the next login is rejected.
		if validToken(r.SessionToken) {
			if err := tx.RevokeSession(Digest(r.SessionToken)); err != nil {
				return err
			}
		}
		var deviceID string
		if pin {
			d, err := tx.Device(Digest(r.DeviceToken))
			if err != nil && !errors.Is(err, ErrMissing) {
				return err
			}
			if err != nil || d.Revoked || !d.Expires.After(time.Now()) {
				denied = Fail("DEVICE_REQUIRED", 403)
				return tx.Audit(Event{Action: "login", RequestID: r.RequestID, Reason: "DEVICE_REQUIRED"})
			}
			deviceID = d.ID
		}
		keys := s.rateKeys(r.Source, "login:"+strings.ToLower(subject))
		locked, err := blocked(tx, keys)
		if err != nil {
			return err
		}
		if locked {
			denied = Fail("AUTH_BLOCKED", 429)
			return tx.Audit(Event{Action: "login", DeviceID: deviceID, RequestID: r.RequestID, Reason: "AUTH_BLOCKED"})
		}
		var u User
		if pin && ValidID(subject) {
			u, err = tx.User(subject)
		} else if !pin {
			u, err = tx.UserByLogin(strings.ToLower(subject))
		} else {
			err = ErrMissing
		}
		if err != nil && !errors.Is(err, ErrMissing) {
			return err
		}
		hash := u.PasswordHash
		if pin {
			hash = u.PINHash
		}
		if hash == "" {
			hash = s.dummy
		}
		valid := s.secrets.Verify(hash, secret, pin)
		if err != nil || !u.Active || !valid || (pin && (!ValidPIN(secret) || !u.Has("admin"))) || (!pin && !u.Has("manager")) {
			if err := failed(tx, keys); err != nil {
				return err
			}
			denied = Fail("INVALID_CREDENTIALS", 401)
			return tx.Audit(Event{ActorID: u.ID, DeviceID: deviceID, Action: "login", RequestID: r.RequestID, Reason: "INVALID_CREDENTIALS"})
		}
		token, csrf := Token(), Token()
		now := time.Now()
		session := Session{Hash: Digest(token), UserID: u.ID, DeviceID: deviceID, CSRFHash: Digest(csrf), AuthVersion: u.AuthVersion, Idle: now.Add(15 * time.Minute), Absolute: now.Add(14 * time.Hour)}
		if err := tx.SaveSession(session); err != nil {
			return err
		}
		// Reset the subject counter only; a successful login must not clear a source's spray history.
		if err := tx.SaveAttempt(Attempt{Key: keys[1], Start: now, Blocked: now}); err != nil {
			return err
		}
		result = LoginResult{Identity: Identity{User: u, DeviceTrusted: deviceID != "", PINSession: deviceID != "", Session: session}, Token: token, CSRF: csrf}
		return audit(tx, r, result.Identity, "login", u.ID, true, "")
	})
	if err != nil {
		return LoginResult{}, err
	}
	return result, denied
}
func (s *Service) Logout(ctx context.Context, r Request) error {
	return s.authenticated(ctx, r, "", true, func(tx Tx, id Identity) error {
		if err := tx.RevokeSession(id.Session.Hash); err != nil {
			return err
		}
		return audit(tx, r, id, "logout", id.User.ID, true, "")
	})
}
func (s *Service) Activity(ctx context.Context, r Request) error {
	return s.authenticated(ctx, r, "", true, func(tx Tx, id Identity) error {
		id.Session.Idle = time.Now().Add(15 * time.Minute)
		if id.Session.Idle.After(id.Session.Absolute) {
			id.Session.Idle = id.Session.Absolute
		}
		return tx.SaveSession(id.Session)
	})
}

func validateInput(in UserInput) bool {
	if n := utf8.RuneCountInString(strings.TrimSpace(in.Name)); n < 1 || n > 100 {
		return false
	}
	if len(in.Roles) == 0 || len(in.Roles) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, role := range in.Roles {
		if seen[role] || (role != "admin" && role != "manager") {
			return false
		}
		seen[role] = true
	}
	if seen["admin"] && !ValidPIN(in.PIN) {
		return false
	}
	if seen["manager"] {
		if !ValidPassword(in.Password) || len(in.Login) < 3 || len(in.Login) > 100 {
			return false
		}
		for _, c := range in.Login {
			if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789._-", c) {
				return false
			}
		}
	}
	return true
}
func (s *Service) newUser(in UserInput) User {
	u := User{Name: strings.TrimSpace(in.Name), Login: in.Login, Active: true, Roles: in.Roles, Version: 1, AuthVersion: 1}
	if u.Has("admin") {
		u.PINHash = s.secrets.Hash(in.PIN, true)
	}
	if u.Has("manager") {
		u.PasswordHash = s.secrets.Hash(in.Password, false)
	}
	return u
}
func (s *Service) Bootstrap(ctx context.Context, in UserInput) error {
	if !validateInput(in) || len(in.Roles) != 1 || in.Roles[0] != "manager" {
		return Fail("INVALID_INPUT", 400)
	}
	return s.repo.Within(ctx, func(tx Tx) error {
		users, err := tx.Users()
		if err != nil {
			return err
		}
		if len(users) != 0 {
			return Fail("BOOTSTRAP_ALREADY_DONE", 409)
		}
		u := s.newUser(in)
		if err := tx.SaveUser(&u); err != nil {
			return err
		}
		return tx.Audit(Event{ActorID: u.ID, Action: "manager_bootstrap", EntityID: u.ID, RequestID: "local-cli", Success: true})
	})
}
func (s *Service) Users(ctx context.Context, r Request) ([]User, error) {
	var users []User
	err := s.authenticated(ctx, r, "manager", false, func(tx Tx, _ Identity) error { var err error; users, err = tx.Users(); return err })
	return users, err
}
func (s *Service) CreateUser(ctx context.Context, r Request, in UserInput) (User, error) {
	if !validateInput(in) {
		return User{}, Fail("INVALID_INPUT", 400)
	}
	var u User
	err := s.authenticated(ctx, r, "manager", true, func(tx Tx, id Identity) error {
		u = s.newUser(in)
		if err := tx.SaveUser(&u); err != nil {
			return err
		}
		return audit(tx, r, id, "user_created", u.ID, true, "")
	})
	return u, err
}
func (s *Service) UpdateUser(ctx context.Context, r Request, userID, name string, active bool, version int64) (User, error) {
	if !ValidID(userID) || utf8.RuneCountInString(strings.TrimSpace(name)) < 1 || utf8.RuneCountInString(name) > 100 {
		return User{}, Fail("INVALID_INPUT", 400)
	}
	var u User
	err := s.authenticated(ctx, r, "manager", true, func(tx Tx, id Identity) error {
		var err error
		u, err = tx.User(userID)
		if err != nil {
			return err
		}
		if u.Version != version {
			return Fail("VERSION_CONFLICT", 409)
		}
		if u.Active && !active && u.Has("manager") {
			users, err := tx.Users()
			if err != nil {
				return err
			}
			count := 0
			for _, v := range users {
				if v.Active && v.Has("manager") {
					count++
				}
			}
			if count <= 1 {
				return Fail("LAST_MANAGER", 422)
			}
		}
		before := map[string]any{"display_name": u.Name, "active": u.Active, "version": u.Version}
		if u.Active && !active {
			protected, err := tx.UserHasUnfinishedShift(u.ID)
			if err != nil {
				return err
			}
			if protected {
				return Fail("USER_HAS_UNFINISHED_SHIFT", 422)
			}
		}
		u.Name = strings.TrimSpace(name)
		u.Active = active
		u.Version++
		if !active {
			u.AuthVersion++
			if err := tx.RevokeUserSessions(u.ID); err != nil {
				return err
			}
		}
		if err := tx.SaveUser(&u); err != nil {
			return err
		}
		return tx.Audit(Event{ActorID: id.User.ID, DeviceID: id.Session.DeviceID, Action: "user_updated", EntityID: u.ID, RequestID: r.RequestID, Success: true, Details: map[string]any{"before": before, "after": map[string]any{"display_name": u.Name, "active": u.Active, "version": u.Version}}})
	})
	return u, err
}
func (s *Service) ResetPIN(ctx context.Context, r Request, userID, pin string, version int64) error {
	if !ValidPIN(pin) || !ValidID(userID) {
		return Fail("INVALID_INPUT", 400)
	}
	return s.authenticated(ctx, r, "manager", true, func(tx Tx, id Identity) error {
		u, err := tx.User(userID)
		if err != nil {
			return err
		}
		if !u.Has("admin") {
			return Fail("INVALID_INPUT", 400)
		}
		if u.Version != version {
			return Fail("VERSION_CONFLICT", 409)
		}
		u.PINHash = s.secrets.Hash(pin, true)
		u.AuthVersion++
		u.Version++
		if err := tx.SaveUser(&u); err != nil {
			return err
		}
		if err := tx.RevokeUserSessions(u.ID); err != nil {
			return err
		}
		return audit(tx, r, id, "pin_reset", u.ID, true, "")
	})
}

func (s *Service) Enroll(ctx context.Context, r Request, password string) (string, error) {
	var token string
	var denied error
	err := s.authenticated(ctx, r, "manager", true, func(tx Tx, id Identity) error {
		keys := s.rateKeys(r.Source, "reauth:"+id.User.ID)
		locked, err := blocked(tx, keys)
		if err != nil {
			return err
		}
		if locked {
			denied = Fail("AUTH_BLOCKED", 429)
			return audit(tx, r, id, "device_enrollment", "", false, "AUTH_BLOCKED")
		}
		if !s.secrets.Verify(id.User.PasswordHash, password, false) {
			if err := failed(tx, keys); err != nil {
				return err
			}
			denied = Fail("INVALID_CREDENTIALS", 401)
			return audit(tx, r, id, "device_enrollment", "", false, "INVALID_CREDENTIALS")
		}
		token = Token()
		if err := tx.SaveEnrollment(Enrollment{Hash: Digest(token), CreatedBy: id.User.ID, Expires: time.Now().Add(10 * time.Minute)}); err != nil {
			return err
		}
		return audit(tx, r, id, "device_enrollment", "", true, "")
	})
	if err != nil {
		return "", err
	}
	return token, denied
}
func (s *Service) RegisterDevice(ctx context.Context, r Request, code, label string) (string, error) {
	if !validToken(code) || utf8.RuneCountInString(strings.TrimSpace(label)) < 1 || utf8.RuneCountInString(label) > 100 {
		return "", Fail("INVALID_INPUT", 400)
	}
	var token string
	var denied error
	err := s.repo.Within(ctx, func(tx Tx) error {
		keys := s.rateKeys(r.Source, "device-registration")
		locked, err := blocked(tx, keys)
		if err != nil {
			return err
		}
		if locked {
			denied = Fail("AUTH_BLOCKED", 429)
			return tx.Audit(Event{Action: "device_registered", RequestID: r.RequestID, Reason: "AUTH_BLOCKED"})
		}
		e, err := tx.Enrollment(Digest(code))
		if err != nil && !errors.Is(err, ErrMissing) {
			return err
		}
		if err != nil || e.Used || !e.Expires.After(time.Now()) {
			if err := failed(tx, keys); err != nil {
				return err
			}
			denied = Fail("INVALID_ENROLLMENT", 403)
			return tx.Audit(Event{Action: "device_registered", RequestID: r.RequestID, Reason: "INVALID_ENROLLMENT"})
		}
		owner, err := tx.User(e.CreatedBy)
		if err != nil {
			return err
		}
		if !owner.Active || !owner.Has("manager") {
			return Fail("INVALID_ENROLLMENT", 403)
		}
		// Registering a browser replaces its current trust and personal session.
		if validToken(r.DeviceToken) {
			old, err := tx.Device(Digest(r.DeviceToken))
			if err != nil && !errors.Is(err, ErrMissing) {
				return err
			}
			if err == nil {
				old.Revoked = true
				if err := tx.SaveDevice(&old); err != nil {
					return err
				}
			}
		}
		if validToken(r.SessionToken) {
			if err := tx.RevokeSession(Digest(r.SessionToken)); err != nil {
				return err
			}
		}
		token = Token()
		d := Device{Hash: Digest(token), Label: strings.TrimSpace(label), Expires: time.Now().Add(30 * 24 * time.Hour), CreatedBy: e.CreatedBy}
		if err := tx.SaveDevice(&d); err != nil {
			return err
		}
		e.Used = true
		if err := tx.SaveEnrollment(e); err != nil {
			return err
		}
		return tx.Audit(Event{ActorID: e.CreatedBy, DeviceID: d.ID, Action: "device_registered", EntityID: d.ID, RequestID: r.RequestID, Success: true})
	})
	if err != nil {
		return "", err
	}
	return token, denied
}
func (s *Service) Devices(ctx context.Context, r Request) ([]Device, error) {
	var list []Device
	err := s.authenticated(ctx, r, "manager", false, func(tx Tx, _ Identity) error { var err error; list, err = tx.Devices(); return err })
	return list, err
}
func (s *Service) RevokeDevice(ctx context.Context, r Request, deviceID string) error {
	if !ValidID(deviceID) {
		return Fail("INVALID_INPUT", 400)
	}
	return s.authenticated(ctx, r, "manager", true, func(tx Tx, id Identity) error {
		list, err := tx.Devices()
		if err != nil {
			return err
		}
		for _, d := range list {
			if d.ID == deviceID {
				d.Revoked = true
				if err := tx.SaveDevice(&d); err != nil {
					return err
				}
				return audit(tx, r, id, "device_revoked", d.ID, true, "")
			}
		}
		return ErrMissing
	})
}
func (s *Service) Reauth(ctx context.Context, r Request, secret, action, target string, revision int64) (string, error) {
	if !ValidID(target) || revision < 1 || (action != "prepare_handover" && action != "accept_handover" && action != "collection" && action != "reverse_collection") {
		return "", Fail("INVALID_INPUT", 400)
	}
	var token string
	var denied error
	err := s.authenticated(ctx, r, "admin", true, func(tx Tx, id Identity) error {
		if id.Session.DeviceID == "" || !id.DeviceTrusted {
			return Fail("DEVICE_REQUIRED", 403)
		}
		keys := s.rateKeys(r.Source, "reauth:"+id.User.ID)
		locked, err := blocked(tx, keys)
		if err != nil {
			return err
		}
		if locked {
			denied = Fail("AUTH_BLOCKED", 429)
			return audit(tx, r, id, "reauth", target, false, "AUTH_BLOCKED")
		}
		if !ValidPIN(secret) || !s.secrets.Verify(id.User.PINHash, secret, true) {
			if err := failed(tx, keys); err != nil {
				return err
			}
			denied = Fail("INVALID_CREDENTIALS", 401)
			return audit(tx, r, id, "reauth", target, false, "INVALID_CREDENTIALS")
		}
		token = Token()
		if err := tx.SaveProof(Proof{Hash: Digest(token), ActorID: id.User.ID, Action: action, TargetID: target, SessionHash: id.Session.Hash, Revision: revision, Expires: time.Now().Add(2 * time.Minute)}); err != nil {
			return err
		}
		return audit(tx, r, id, "reauth", target, true, "")
	})
	if err != nil {
		return "", err
	}
	return token, denied
}
