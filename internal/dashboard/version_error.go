package dashboard

import (
	"errors"
	"fmt"
)

var ErrVersionNotFound = errors.New("dashboard version not found")
var ErrAlreadyCurrent = errors.New("dashboard version is already current")

const VersionNotFoundCode = "dashboard_version_not_found"
const AlreadyCurrentCode = "already_current"

type versionNotFoundError struct {
	id      string
	version int
}

func (e versionNotFoundError) Error() string {
	return fmt.Sprintf("version %d of dashboard %s does not exist", e.version, e.id)
}
func (versionNotFoundError) Unwrap() error { return ErrVersionNotFound }
