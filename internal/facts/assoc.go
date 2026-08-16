package facts

import "encoding/json"

// Assoc is the pull request author's association with the repository,
// ordered by decreasing trust so ">" means "less trusted". The §12.3
// fork-guard rule relies on this ordering directly:
// facts.pr.is_fork && facts.pr.author_association > ASSOC_COLLABORATOR.
type Assoc int

const (
	AssocOwner Assoc = iota
	AssocMember
	AssocCollaborator
	AssocContributor
	AssocFirstTimeContributor
	AssocNone
)

var assocStrings = [...]string{
	AssocOwner:                "OWNER",
	AssocMember:               "MEMBER",
	AssocCollaborator:         "COLLABORATOR",
	AssocContributor:          "CONTRIBUTOR",
	AssocFirstTimeContributor: "FIRST_TIME_CONTRIBUTOR",
	AssocNone:                 "NONE",
}

// ParseAssoc maps a raw GitHub author_association string to Assoc.
// "FIRST_TIMER" maps to AssocFirstTimeContributor; "MANNEQUIN" and any
// unrecognised value map to AssocNone.
func ParseAssoc(s string) Assoc {
	switch s {
	case "OWNER":
		return AssocOwner
	case "MEMBER":
		return AssocMember
	case "COLLABORATOR":
		return AssocCollaborator
	case "CONTRIBUTOR":
		return AssocContributor
	case "FIRST_TIME_CONTRIBUTOR", "FIRST_TIMER":
		return AssocFirstTimeContributor
	default: // "NONE", "MANNEQUIN", or anything unrecognised.
		return AssocNone
	}
}

// String returns the GitHub association string for a.
func (a Assoc) String() string {
	if a >= 0 && int(a) < len(assocStrings) {
		return assocStrings[a]
	}
	return "NONE"
}

// MarshalJSON encodes a as its GitHub association string.
func (a Assoc) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

// UnmarshalJSON decodes a GitHub association string into a via ParseAssoc.
func (a *Assoc) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*a = ParseAssoc(s)
	return nil
}
