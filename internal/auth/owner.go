package auth

import (
	"errors"
)

// OwnerOutcome reports what DesignateOwner did with a configured owner email.
type OwnerOutcome int

const (
	// OwnerCreated means the control database had no users and the owner was
	// created as an active administrator.
	OwnerCreated OwnerOutcome = iota
	// OwnerPresent means users already existed and one of them has the owner
	// email. The account is returned unchanged, whatever its role or status.
	OwnerPresent
	// OwnerRefused means users already existed and none has the owner email.
	// Nothing was created: the setting designates only the first administrator.
	OwnerRefused
)

// DesignateOwner creates the first administrator from a configured owner
// email. It creates an account only when the control database holds no users,
// using the same transactional check as interactive setup, so it can never
// add a second administrator alongside an existing installation's accounts.
// When users already exist it reports whether the owner is among them and
// changes nothing.
func (s *UserStore) DesignateOwner(rawEmail string) (User, OwnerOutcome, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return User{}, OwnerRefused, err
	}
	user, err := s.CreateFirstAdminWithAudit(email, "", AuditEvent{
		EventType: "setup.completed",
		Outcome:   "success",
		Metadata:  map[string]any{"source": "owner_email"},
	})
	if err == nil {
		return user, OwnerCreated, nil
	}
	if !errors.Is(err, ErrSetupComplete) {
		return User{}, OwnerRefused, err
	}
	existing, err := s.GetByEmail(email)
	if errors.Is(err, ErrUserNotFound) {
		return User{}, OwnerRefused, nil
	}
	if err != nil {
		return User{}, OwnerRefused, err
	}
	return existing, OwnerPresent, nil
}
