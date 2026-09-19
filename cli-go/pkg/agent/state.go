package agent

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
)

// GrantView is one Rosie record as a person sees it.
type GrantView struct {
	URL         string `json:"url"`
	Behaviours  string `json:"behaviours"`
	Basis       string `json:"basis"`
	Since       string `json:"since"`
	State       string `json:"state"` // "live" | "withdrawn" | "unverified"
	WithdrawnAt string `json:"withdrawn_at,omitempty"`
	Withdrawal  string `json:"withdrawal,omitempty"`
}

// State is what Settings → Rosie shows.
type State struct {
	// On is whether a live grant covering blessing stands.
	On bool `json:"on"`
	// Live is the grant Rosie acts under, when she is on.
	Live *GrantView `json:"live,omitempty"`
	// Never is true when the site has no Rosie record at all.
	Never bool `json:"never"`
	// OffSince is when she was last switched off, when she is off after having
	// been on.
	OffSince string `json:"off_since,omitempty"`
	// History is every Rosie record, oldest first.
	History []GrantView `json:"history"`
}

// ReadState reads Rosie's state from the site's own records.
func ReadState(siteDir string) (*State, error) {
	grants, err := attestation.Grants(siteDir, Rosie)
	if err != nil {
		return nil, err
	}
	st := &State{Never: len(grants) == 0, History: []GrantView{}}
	live, _ := LiveRosieGrant(siteDir)
	for _, g := range grants {
		v := GrantView{
			URL: g.URL, Behaviours: g.Record.Payload[attestation.GrantKeyBehaviours], Basis: g.Basis,
			Since: g.Record.Asserted, State: "live",
		}
		switch {
		case g.Withdrawn:
			v.State, v.WithdrawnAt, v.Withdrawal = "withdrawn", g.WithdrawnAt, g.WithdrawalURL
			if g.WithdrawnAt > st.OffSince {
				st.OffSince = g.WithdrawnAt
			}
		case g.Signature != attestation.StatusValid:
			v.State = "unverified"
		}
		if live.Valid() && g.URL == live.URL() {
			vv := v
			st.Live = &vv
		}
		st.History = append(st.History, v)
	}
	st.On = live.Valid()
	if st.On {
		st.OffSince = ""
	}
	return st, nil
}
