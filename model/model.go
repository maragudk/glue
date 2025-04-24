package model

import "fmt"

type ID string

// String satisfies [fmt.Stringer].
func (i ID) String() string {
	return string(i)
}

var _ fmt.Stringer = ID("")
