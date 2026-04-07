package memory

import "time"

var statusWeights = map[Status]float64{
	StatusCanonical:  1.0,
	StatusReviewed:   0.9,
	StatusDraft:      0.6,
	StatusDeprecated: 0.1,
}

var originWeights = map[Origin]float64{
	OriginFeedback:    1.3,
	OriginUser:        1.1,
	OriginProject:     1.0,
	OriginReference:   0.9,
	OriginObservation: 0.8,
}

func recencyFactor(lastAccessed *time.Time, now time.Time) float64 {
	if lastAccessed == nil {
		return 0.75
	}
	days := now.Sub(*lastAccessed).Hours() / 24
	if days <= 0 {
		return 1.0
	}
	if days >= 30 {
		return 0.5
	}
	return 1.0 - 0.5*(days/30)
}

func activationScore(rev Revision, state State, now time.Time) float64 {
	sw := statusWeights[rev.Status]
	ow := originWeights[rev.Origin]
	rf := recencyFactor(state.LastAccessedAt, now)
	return state.Activation * sw * rev.Confidence * ow * rf
}

func chronologicalKey(rev Revision) int64 {
	return rev.CreatedAt.UnixNano()
}
