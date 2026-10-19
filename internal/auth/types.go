package auth

import (
	"context"
	"errors"
	"time"
)

var ErrMissing = errors.New("record missing")

type Failure struct {
	Code   string
	Status int
}

func (e *Failure) Error() string         { return e.Code }
func Fail(code string, status int) error { return &Failure{code, status} }

type User struct {
	ID           string   `json:"id"`
	Name         string   `json:"display_name"`
	Login        string   `json:"manager_login"`
	Active       bool     `json:"active"`
	Roles        []string `json:"roles"`
	Version      int64    `json:"version"`
	AuthVersion  int64    `json:"-"`
	PINHash      string   `json:"-"`
	PasswordHash string   `json:"-"`
}

func (u User) Has(role string) bool {
	for _, r := range u.Roles {
		if r == role {
			return true
		}
	}
	return false
}

type Session struct {
	Hash, UserID, DeviceID, CSRFHash string
	AuthVersion                      int64
	Idle, Absolute                   time.Time
	Revoked                          bool
}
type Device struct {
	ID        string    `json:"id"`
	Hash      string    `json:"-"`
	Label     string    `json:"label"`
	Expires   time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
	CreatedBy string    `json:"-"`
}
type Enrollment struct {
	Hash, CreatedBy string
	Expires         time.Time
	Used            bool
}
type Attempt struct {
	Key            string
	Count          int
	Start, Blocked time.Time
}
type Proof struct {
	Hash, ActorID, Action, TargetID, SessionHash string
	Revision                                     int64
	Expires                                      time.Time
}
type Event struct {
	ActorID, DeviceID, Action, EntityID, RequestID, Reason string
	Success                                                bool
	Details                                                map[string]any
}

// Tx exposes domain records; neither HTTP nor SQL types cross this boundary.
type Tx interface {
	Users() ([]User, error)
	User(id string) (User, error)
	UserByLogin(login string) (User, error)
	SaveUser(user *User) error
	Session(hash string) (Session, error)
	SaveSession(Session) error
	RevokeSession(hash string) error
	RevokeUserSessions(userID string) error
	UserHasUnfinishedShift(userID string) (bool, error)
	Device(hash string) (Device, error)
	Devices() ([]Device, error)
	SaveDevice(*Device) error
	Enrollment(hash string) (Enrollment, error)
	SaveEnrollment(Enrollment) error
	Attempt(key string) (Attempt, error)
	SaveAttempt(Attempt) error
	SaveProof(Proof) error
	ConsumeProof(Proof) (bool, error)
	Audit(Event) error
}

// Within holds the security lock and commits fn atomically, including audit.
// Returning an error rolls back all writes. Rejected auth attempts instead commit
// their counters and audit, then return a Failure outside the transaction.
type Repository interface {
	Within(context.Context, func(Tx) error) error
}

type Request struct{ SessionToken, DeviceToken, CSRF, ActorID, RequestID, Source string }
type Identity struct {
	User          User    `json:"user"`
	DeviceTrusted bool    `json:"device_trusted"`
	PINSession    bool    `json:"pin_session"`
	Session       Session `json:"-"`
}
type LoginResult struct {
	Identity    Identity
	Token, CSRF string
}
type UserInput struct {
	Name     string   `json:"display_name"`
	Login    string   `json:"manager_login"`
	Roles    []string `json:"roles"`
	PIN      string   `json:"pin"`
	Password string   `json:"password"`
}
