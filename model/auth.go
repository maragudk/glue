package model

import "fmt"

// Role for a user. A user may have 0 to many of these.
type Role string

// String satisfies [fmt.Stringer].
func (r Role) String() string {
	return string(r)
}

var _ fmt.Stringer = Role("")

// Permission for a user used during authorization.
type Permission string

// String satisfies [fmt.Stringer].
func (p Permission) String() string {
	return string(p)
}

var _ fmt.Stringer = Permission("")
