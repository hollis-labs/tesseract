package memory

import "time"

var statusWeights = map[Status]float64{
	StatusCanonical:  1.0,
	StatusReviewed:   0.9,
	StatusDraft:      0.6,
	StatusDeprecated: 0.1,
}

var derivedFromWeights = map[DerivedFrom]float64{
	DerivedFromFeedback:    1.3,
	DerivedFromUser:        1.1,
	DerivedFromProject:     1.0,
	DerivedFromReference:   0.9,
	DerivedFromObservation: 0.8,
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
	ow := derivedFromWeights[rev.DerivedFrom]
	rf := recencyFactor(state.LastAccessedAt, now)
	return state.Activation * sw * rev.Confidence * ow * rf
}

func chronologicalKey(rev Revision) int64 {
	return rev.CreatedAt.UnixNano()
}
