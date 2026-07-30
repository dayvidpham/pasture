package receipt

import "time"

const DefaultIngressDeadline = time.Second

type Clock interface{ Now() time.Time }

type OperationIDSource interface {
	NewOperationID() (string, error)
}
