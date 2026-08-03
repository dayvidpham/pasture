package receipt

import "time"

type Clock interface{ Now() time.Time }

type OperationIDSource interface {
	NewOperationID() (string, error)
}
