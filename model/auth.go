package model

type User struct {
	ID        ID
	Created   Time
	Updated   Time
	Name      string
	Email     EmailAddress
	Confirmed bool
	Active    bool
}
