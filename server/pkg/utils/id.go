package utils

import (
	uuid "github.com/gofrs/uuid/v5"
)

// namespace pins UUIDv5 output so the same input always maps to the
// same UUID across instances and restarts.
var namespace = uuid.Must(uuid.FromString("f47ac10b-58cc-4372-a567-0e02b2c3d479"))

// NewUUIDv5 derives a deterministic UUID from input — same input
// always returns the same UUID. Used for content-addressed article
// IDs derived from the article URL.
func NewUUIDv5(input string) string {
	return uuid.NewV5(namespace, input).String()
}

// NewUuidv4 returns a random UUID. Returns "" if the crypto source
// failed (treat as fatal at the call site; the v4 generator should
// not fail in normal operation).
func NewUuidv4() string {
	id, err := uuid.NewV4()
	if err != nil {
		return ""
	}

	return id.String()
}

// NewArticleID picks the UUID flavour appropriate for an article:
// v5 (deterministic) when a URL is supplied so re-fetches dedupe,
// v4 (random) when there is no URL to seed from.
func NewArticleID(input string) string {
	switch input {
	case "":
		id, err := uuid.NewV4()
		if err != nil {
			return ""
		}

		return id.String()

	default:
		return uuid.NewV5(namespace, input).String()
	}
}
