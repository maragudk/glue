package model

import (
	"fmt"
	"regexp"
	"strings"
)

// emailAddressMatcher for valid email addresses.
// See https://regex101.com/r/1BEPJo/latest for an interactive breakdown of the regexp.
// See https://html.spec.whatwg.org/#valid-e-mail-address for the definition.
var emailAddressMatcher = regexp.MustCompile(
	// Start of string
	`^` +
		// Local part of the address. Note that \x60 is a backtick (`) character.
		`(?P<local>[a-zA-Z0-9.!#$%&'*+/=?^_\x60{|}~-]+)` +
		`@` +
		// Domain of the address
		`(?P<domain>[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+)` +
		// End of string
		`$`,
)

type EmailAddress string

func (e EmailAddress) IsValid() bool {
	return emailAddressMatcher.MatchString(string(e))
}

func (e EmailAddress) String() string {
	return string(e)
}

var _ fmt.Stringer = EmailAddress("")

func (e EmailAddress) Local() string {
	return emailAddressMatcher.FindStringSubmatch(string(e))[1]
}

func (e EmailAddress) ToLower() EmailAddress {
	return EmailAddress(strings.TrimSpace(strings.ToLower(string(e))))
}

type Keywords = map[string]string
