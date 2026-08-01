package waist

import "encoding/json"

type ConsultationLegalized interface {
	json.Marshaler
	IsValid() bool
	ConsultationLegalized()
}

type ConsultationResponse interface {
	json.Marshaler
	IsValid() bool
	ConsultationResponse()
}
