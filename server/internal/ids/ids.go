// Package ids generates identifiers.
package ids

import "github.com/google/uuid"

// NewV7 returns a time-ordered UUIDv7.
func NewV7() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic("ids: uuid v7: " + err.Error())
	}
	return id
}
