package v1

// Status is the rollup state used in /api/v1/system responses so the
// dashboard renders a single coloured indicator.
type Status string

const (
	Online  Status = "online"
	Error   Status = "error"
	Offline Status = "offline"
)
