package model

// Error is for errors in the business domain. See the constants below.
type Error string

const (
	ErrorUserNotFound = Error("user not found")
)

// Error satisfies [error].
func (e Error) Error() string {
	return string(e)
}

var _ error = Error("")
