package model

import "fmt"

type ID string

// String satisfies [fmt.Stringer].
func (i ID) String() string {
	return string(i)
}

var _ fmt.Stringer = ID("")

type AccountID ID

// String satisfies [fmt.Stringer].
func (i AccountID) String() string {
	return string(i)
}

var _ fmt.Stringer = AccountID("")

type UserID ID

// String satisfies [fmt.Stringer].
func (i UserID) String() string {
	return string(i)
}

var _ fmt.Stringer = UserID("")
