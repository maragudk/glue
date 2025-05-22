package model

import "fmt"

type Role string

// String satisfies [fmt.Stringer].
func (r Role) String() string {
	return string(r)
}

var _ fmt.Stringer = Role("")

type Permission string

// String satisfies [fmt.Stringer].
func (p Permission) String() string {
	return string(p)
}

var _ fmt.Stringer = Permission("")
