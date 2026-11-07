package auth

import (
	"errors"
)

// FirstAdminOutcome reports what DesignateFirstAdmin did with a configured
// first administrator email.
type FirstAdminOutcome int

const (
	// FirstAdminUnknown is the zero value, returned with every error: nothing
	// is known about the account and no outcome should be acted on.
	FirstAdminUnknown FirstAdminOutcome = iota
	// FirstAdminCreated means the control database had no users and the
	// address was created as an active administrator.
	FirstAdminCreated
	// FirstAdminPresent means users already existed and one of them has the
	// configured email. The account is returned unchanged, whatever its role or status.
	FirstAdminPresent
	// FirstAdminRefused means users already existed and none has the
	// configured email.
	// Nothing was created: the setting designates only the first administrator.
	FirstAdminRefused
)

// DesignateFirstAdmin creates the first administrator from a configured
// email. It creates an account only when the control database holds no users,
// using the same transactional check as interactive setup, so it can never
// add a second administrator alongside an existing installation's accounts.
// When users already exist it reports whether the address is among them and
// changes nothing. FirstAdminRefused returns the normalized email in
// User.Email so callers can report it; no such account exists.
func (s *UserStore) DesignateFirstAdmin(rawEmail string) (User, FirstAdminOutcome, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return User{}, FirstAdminUnknown, err
	}
	user, err := s.CreateFirstAdminWithAudit(email, "", AuditEvent{
		EventType: "setup.completed",
		Outcome:   "success",
		Metadata:  map[string]any{"source": "first_admin_email"},
	})
	if err == nil {
		return user, FirstAdminCreated, nil
	}
	if !errors.Is(err, ErrSetupComplete) {
		return User{}, FirstAdminUnknown, err
	}
	existing, err := s.GetByEmail(email)
	if errors.Is(err, ErrUserNotFound) {
		return User{Email: email}, FirstAdminRefused, nil
	}
	if err != nil {
		return User{}, FirstAdminUnknown, err
	}
	return existing, FirstAdminPresent, nil
}
