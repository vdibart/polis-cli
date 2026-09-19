// Package agent is Rosie: the first USER agent (Signet epic 11, designed in
// epic 46).
//
// ⭐ Rosie is software polis provides that works for the user. She acts under
// the user's own grant record, signs with the user's key, and marks every act —
// inside the signature — with her name and that record's URL.
//
// ⛔ THE ARTIFACTS ARE GENERAL; THIS CODE IS ROSIE'S ALONE (M2). The grant
// predicate, its writer rule and live-grant resolution live in pkg/attestation,
// where any agent could use them. Everything here is hardcoded to one agent and
// one behaviour set, and should stay that way until a second agent exists.
//
// ⛔ ROSIE IS NOT AN OPERATOR ACTOR. She has no key, no site and no registry
// entry. The cache upkeep that used to carry her name is operator work and left
// her in epic 21.8: it is Medic's now, and runs in the hosted service.
package agent

import (
	"errors"
	"fmt"
)

// Rosie's identity, as it appears in a grant.
const (
	// Rosie is the agent name a grant carries.
	Rosie = "rosie"
	// RosieProvider is who supplies the software — a claim, never authority.
	RosieProvider = "polis.pub"
	// RosieSet is the name of Rosie's behaviour set.
	RosieSet = "rosie"
	// RosieVersion is the current behaviour-set version.
	//
	// ⚠️ Bump it ONLY for a new behaviour or one that materially changes what she
	// decides — never for a bug fix (epic 46 R15). A bump makes the hosted
	// default pass issue a new grant to every tenant with Rosie active and no
	// withdrawal ever.
	//
	// rosie/1 = comment blessing: grant and deny, under the user's own rules.
	RosieVersion = 1
	// BlessingMinVersion is the first version whose set includes blessing.
	BlessingMinVersion = 1
)

// RosieBehaviours is the current behaviour set, e.g. "rosie/1".
func RosieBehaviours() string { return fmt.Sprintf("%s/%d", RosieSet, RosieVersion) }

// ErrNoLiveGrant is returned by every signing path that is reached without a
// live grant. It is a bug in the caller, never a user state: the caller should
// have resolved a grant and stopped when there was none.
var ErrNoLiveGrant = errors.New("no live grant: an agent act is never signed without one")
