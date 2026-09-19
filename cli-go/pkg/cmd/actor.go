package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/actor"
	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// `polis actor` — an OPERATOR's registry of the system actors it runs.
//
// ⛔ EVERY SUBCOMMAND HERE IS RUN FROM THE OPERATOR'S OWN SITE, and signs with
// the operator's key. That is not a limitation of the tool; it is what the
// artifact means. A registry is one party's statement about actors it runs, and
// an operator that could publish someone else's registry would be publishing a
// claim it has no standing to make.
//
// ⭐ A SELF-HOSTER RUNNING THEIR OWN ACTORS IS AN OPERATOR. Nothing in this
// command knows about polis.pub, and that is deliberate: the registry check
// works identically self-hosted, which is what keeps "you can always leave" a
// real answer rather than a slogan.

func handleActor(args []string) {
	if len(args) < 1 {
		printActorUsage()
		os.Exit(1)
	}
	sub, subArgs := args[0], args[1:]
	switch sub {
	case "register":
		handleActorRegister(subArgs)
	case "withdraw":
		handleActorWithdraw(subArgs)
	case "list":
		handleActorList()
	case "verify":
		handleActorVerify(subArgs)
	case "declare-operator":
		handleActorDeclareOperator(subArgs)
	case "announce":
		handleActorAnnounce(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown actor subcommand: %s\n", sub)
		printActorUsage()
		os.Exit(1)
	}
}

func printActorUsage() {
	fmt.Print(`Polis Actor - the registry of system actors this operator runs

An operator that runs software acting on the network publishes a signed list of
it: which actors are ours, whose authority each exercises, and what we EXPECT
each to do. Without it, anyone can stand up a lookalike and claim to be one.

  EXPECTED, NEVER ALLOWED. This is not an allow-list and nothing enforces it.
  The operator holds its actors' keys by definition, so no published list can
  stop one doing anything. What it buys is that deviation is OBSERVABLE by
  anyone, and the remedy is social. If that tradeoff is not acceptable to
  someone, they can self-host — which is why the exit has to keep working.

Usage:
  polis actor <subcommand> [options]

Subcommands:
  register   Add or update an actor in this operator's registry (no-op on a site that is not already an operator)
  withdraw   Remove an actor, and say so permanently
  list       Show the registry
  verify     Check the registry's signature and each countersignature
  announce   Announce an already-registered actor to the network, and nothing else

Run from an ACTOR's own site, not the operator's:
  declare-operator <domain>     Name the operator accountable for this actor

Run from ANYWHERE — no site, no key, nothing but HTTPS (the stranger's check):
  verify <action-url-or-file>   Given a signed attestation, check whether its
                                signer is a listed actor of its operator, whether
                                the actor countersigned its entry, and whether the
                                action is among its expected actions
    --operator <domain>         Ask this operator, instead of the one the signer
                                claims — catches a lookalike that claims nobody

  Findings are reported SEPARATELY, never as one verdict: a LOOKALIKE (the
  operator does not list the signer), an invalid COUNTERSIGNATURE (the entry
  changed since the actor agreed to it), and a DEVIATION (a listed actor did
  something off its list — information, not a blocked operation).

  verify --custody <domain>     A site's OWN check of custody — run it from your
                                own machine. Who did this? (your events at a
                                discovery service, and who signed each) What does
                                this helper do? (your operator's signed custody
                                declaration about you) What did I allow? (your
                                custody grants, and whether each was withdrawn)
    --operator <domain>         Read this operator's declarations (default: the
                                operator your newest standing grant names)
    --ds <url>                  Discovery service to read events from (default:
                                DISCOVERY_SERVICE_URL, else https://ds.polis.pub)

  Custody is made VISIBLE here, not reduced. Records are verified against keys
  fetched from the sites that signed them, so an operator can withhold them but
  cannot forge one that passes; the event list is the service's own recording.

Register options:
  --authority <operator|user>   Whose authority the actor exercises (required)
  --expects <type>              An action type expected of it; repeatable
  --countersign-with <dir>      The actor's own site directory, whose key
                                countersigns its entry
  --no-attest                   Skip the permanent agent-disclosure record
  --no-announce                 Skip the network announcement

Withdraw options:
  --no-attest / --no-announce   As above

Three artifacts, one fact, three lifetimes:
  the FILE          current state — what is true now, in one fetch
  the ATTESTATIONS  permanent — what was claimed, and when
  the ds_events     delivery — it tells people who were not looking

  The event is NOT authoritative. It says "go look"; the file says what is true.

Examples:
  polis actor register judge.polis.pub --authority operator \
      --expects pub.polis.attestation.integrity
  polis actor withdraw judge.polis.pub
  polis actor verify --tenants-dir /data/tenants
  polis actor verify https://judge.example/content/pub.polis.core/attestation/<id>.json
  polis actor verify ./record.json --operator polis.polis.pub
  polis actor register judge.polis.pub --authority operator --no-announce
  polis actor announce judge.polis.pub     # once the file is verified live
  polis actor declare-operator polis.polis.pub    # from the actor's site
`)
}

type actorRegisterFlags struct {
	domain          string
	authority       string
	expects         []string
	countersignWith string
	noAttest        bool
	noAnnounce      bool
}

func parseActorRegisterFlags(args []string) (*actorRegisterFlags, error) {
	f := &actorRegisterFlags{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case a == "--authority":
			f.authority, err = next()
		case a == "--expects":
			var v string
			v, err = next()
			if err == nil {
				f.expects = append(f.expects, v)
			}
		case a == "--countersign-with":
			f.countersignWith, err = next()
		case a == "--no-attest":
			f.noAttest = true
		case a == "--no-announce":
			f.noAnnounce = true
		case strings.HasPrefix(a, "-"):
			err = fmt.Errorf("unknown flag: %s", a)
		default:
			if f.domain != "" {
				err = fmt.Errorf("unexpected argument: %s", a)
			}
			f.domain = a
		}
		if err != nil {
			return nil, err
		}
	}
	if f.domain == "" {
		return nil, fmt.Errorf("usage: polis actor register <domain> --authority <operator|user> [--expects <type>]")
	}
	if strings.Contains(f.domain, "/") || strings.Contains(f.domain, ":") {
		return nil, fmt.Errorf("%q looks like a URL — an entry names a DOMAIN, so that it survives a key rotation", f.domain)
	}
	switch f.authority {
	case actor.AuthorityOperator, actor.AuthorityUser:
	case "":
		return nil, fmt.Errorf("--authority is required: %q for an actor acting on the operator's own authority, %q for one exercising a tenant's",
			actor.AuthorityOperator, actor.AuthorityUser)
	default:
		// ⛔ NOT "handle". The values are the already-published authority rule's
		// two words; a third word for the same concept is vocabulary drift.
		return nil, fmt.Errorf("--authority must be %q or %q, got %q",
			actor.AuthorityOperator, actor.AuthorityUser, f.authority)
	}
	for _, e := range f.expects {
		if !strings.Contains(e, ".") {
			return nil, fmt.Errorf("expected action %q must be fully qualified, e.g. pub.polis.attestation.integrity", e)
		}
	}
	return f, nil
}

// operatorIdentity returns the operator site's own domain, or exits.
//
// ⛔ THE `operator` FIELD IS THE SITE ORIGIN, not the party name. A verifier
// FETCHES it: https://<operator>/.well-known/polis is where the key that signed
// the registry is published. Naming the parent brand there would point a
// verifier at a document that does not carry the signing key.
func operatorIdentity() string {
	if strings.TrimSpace(baseURL) == "" {
		exitError("POLIS_BASE_URL is not set — a registry must name the origin its signature binds to")
	}
	domain := polisurl.ExtractDomain(baseURL)
	if domain == "" {
		exitError("could not derive a domain from POLIS_BASE_URL=%q", baseURL)
	}
	return domain
}

// registerDecision is what `polis actor register` decides before it touches
// anything. It lives apart from the handler because exitError calls os.Exit, so
// a gate inlined there could never be tested in-process.
type registerDecision int

const (
	// registerProceed — the site publishes an actor_registry pointer: an operator.
	registerProceed registerDecision = iota
	// registerNoop — no pointer and no registry file: an ordinary site. Most sites
	// run no actors, so this is the common case.
	registerNoop
	// registerBroken — a registry file but no pointer: a broken operator, not an
	// ordinary site.
	registerBroken
)

// registerGate decides whether `polis actor register` may write at all.
//
// ⛔ Registering actors is OPERATOR ADMINISTRATION, and it sits in a CLI anyone
// can run. loadOrCreateRegistry used to let a single run on any site with a key
// create a registry — and, through the package write that sets the pointer, turn
// that site into an operator. Until admin commands are segmented (to be revisited
// before operator tooling is open-sourced), register no-ops on every site that is
// not already an operator.
//
// ⛔ The ALLOW decision keys on the actor_registry POINTER, never on the file. A
// site whose registry file is gone but whose pointer survives is the documented
// recovery case — re-running register rebuilds it — and a file-keyed gate would
// break exactly that.
func registerGate(dataDir string) registerDecision {
	if site.ActorRegistryPointer(dataDir) != "" {
		return registerProceed
	}
	if _, err := os.Stat(actor.RegistryPath(dataDir)); err == nil {
		return registerBroken
	}
	return registerNoop
}

func loadOrCreateRegistry(dataDir, operator string) *actor.Registry {
	if r, err := actor.Load(actor.RegistryPath(dataDir)); err == nil {
		r.Operator = operator
		return r
	}
	return &actor.Registry{V: actor.SchemaVersion, Operator: operator, Actors: []actor.Entry{}}
}

func handleActorRegister(args []string) {
	f, err := parseActorRegisterFlags(args)
	if err != nil {
		exitError("%v", err)
	}
	dataDir := getDataDir()

	switch registerGate(dataDir) {
	case registerNoop:
		reason := "This site is not an operator (it publishes no actor_registry pointer), so polis actor register does nothing here: registering actors is operator administration."
		if jsonOutput {
			outputJSON(map[string]interface{}{
				"status":  "success",
				"command": "actor",
				"data": map[string]interface{}{
					"noop":   true,
					"domain": f.domain,
					"reason": reason,
				},
			})
		} else {
			fmt.Fprintln(os.Stderr, reason)
		}
		return
	case registerBroken:
		exitError("this site has an actor registry file but publishes no actor_registry pointer — a broken operator, not an ordinary site; restore the pointer before registering (refusing to write)")
	}

	operator := operatorIdentity()

	privKey, err := loadPrivateKey(dataDir)
	if err != nil {
		exitError("Failed to load the operator's private key: %v", err)
	}
	defer signing.ZeroKey(privKey)

	r := loadOrCreateRegistry(dataDir, operator)
	existing := r.Find(f.domain)
	updating := existing != nil

	entry := actor.Entry{
		Domain:          f.domain,
		Authority:       f.authority,
		ExpectedActions: f.expects,
	}
	if entry.ExpectedActions == nil {
		entry.ExpectedActions = []string{}
	}

	// ⭐ THE COUNTERSIGNATURE COVERS THE ENTRY, and the asymmetry is the point:
	// an operator cannot unilaterally WIDEN what it claims an actor does,
	// because widening breaks the countersignature until the actor re-signs.
	// Narrowing and removal need no consent — a decommissioned or compromised
	// actor cannot sign, and you must always be able to disown one.
	if f.countersignWith != "" {
		actorKey, err := loadPrivateKey(f.countersignWith)
		if err != nil {
			exitError("Failed to read the actor's key from %s: %v", f.countersignWith, err)
		}
		defer signing.ZeroKey(actorKey)
		// The entry must be in the registry before it is countersigned: the
		// signed bytes bind the operator that is listing it, or the signature
		// would be replayable into somebody else's registry.
		staged := *r
		staged.Actors = upsert(r.Actors, entry)
		sig, err := actor.Countersign(&staged, &entry, actorKey)
		if err != nil {
			exitError("Failed to countersign: %v", err)
		}
		entry.Countersignature = sig

		// ⭐ COMPLETE THE TWO-HOP DISCOVERY while we are here. The actor's own
		// .well-known/polis names its operator, and the operator's names the
		// registry: that pair is how a stranger who has only seen a signature
		// from judge.polis.pub reaches the list that vouches for it. Nobody
		// finds a registry by guessing an operator's hostname.
		//
		// ⛔ The actor's claim is NOT evidence. It is corroborated by this
		// operator's list containing the domain — never the other way round.
		if err := site.SetOperatorPointer(f.countersignWith, operator); err != nil {
			fmt.Fprintf(os.Stderr, "[!] Could not write the operator pointer on %s: %v\n", f.countersignWith, err)
		}
	}

	r.Actors = upsert(r.Actors, entry)
	r.Asserted = time.Now().UTC().Format(time.RFC3339)
	r.Generator = actor.GetGenerator()

	if err := actor.StateRegistry(dataDir, r, privKey); err != nil {
		exitError("Failed to write the registry: %v", err)
	}

	// (b) THE PERMANENT RECORD. The file says what is true now; this says what
	// was claimed and when, and it survives the DS's 90-day prune.
	attestationID := ""
	if !f.noAttest {
		payload := map[string]string{
			"authority": entry.Authority,
			"operator":  operator,
		}
		if len(entry.ExpectedActions) > 0 {
			payload["expected_actions"] = strings.Join(entry.ExpectedActions, ",")
		}
		rec := &attestation.Record{
			Issuer:    strings.TrimRight(baseURL, "/"),
			Predicate: attestation.PredicateAgentDisclosure,
			Subject: attestation.Subject{
				Type: attestation.SubjectIdentity,
				ID:   "https://" + entry.Domain,
			},
			Payload:  payload,
			Asserted: r.Asserted,
		}
		id, err := attestation.Issue(dataDir, rec, privKey)
		if err != nil {
			exitError("Failed to record the disclosure: %v", err)
		}
		attestationID = id
	}

	// (c) DELIVERY. ⛔ NOT AUTHORITATIVE — the payload carries a POINTER, never
	// the facts, so a consumer that trusts the announcement instead of fetching
	// the file has made the DS a gate rather than a witness.
	eventType := "pub.polis.actor.registered"
	if updating {
		eventType = "pub.polis.actor.reregistered"
	}
	announced := announceActorEvent(eventType, entry.Domain, dataDir, privKey, f.noAnnounce)

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "actor",
			"data": map[string]interface{}{
				"operator":         operator,
				"domain":           entry.Domain,
				"authority":        entry.Authority,
				"expected_actions": entry.ExpectedActions,
				"countersigned":    entry.Countersignature != "",
				"updated":          updating,
				"attestation_id":   attestationID,
				"event":            eventType,
				"announced":        announced,
				"registry_url":     registryURL(operator, dataDir),
			},
		})
		return
	}

	verb := "Registered"
	if updating {
		verb = "Updated"
	}
	fmt.Printf("[✓] %s %s (%s authority)\n", verb, entry.Domain, entry.Authority)
	if len(entry.ExpectedActions) > 0 {
		fmt.Printf("    expects: %s\n", strings.Join(entry.ExpectedActions, ", "))
	} else {
		fmt.Printf("    expects: nothing stated\n")
	}
	if entry.Countersignature != "" {
		fmt.Printf("    countersigned by the actor\n")
	} else {
		fmt.Printf("    not countersigned — this says \"we say this is ours\", not \"and it agrees\"\n")
	}
	fmt.Printf("    %s\n", registryURL(operator, dataDir))
}

func handleActorWithdraw(args []string) {
	domain := ""
	noAttest, noAnnounce := false, false
	for _, a := range args {
		switch {
		case a == "--no-attest":
			noAttest = true
		case a == "--no-announce":
			noAnnounce = true
		case strings.HasPrefix(a, "-"):
			exitError("unknown flag: %s", a)
		default:
			domain = a
		}
	}
	if domain == "" {
		exitError("Usage: polis actor withdraw <domain>")
	}

	dataDir := getDataDir()
	operator := operatorIdentity()

	r, err := actor.Load(actor.RegistryPath(dataDir))
	if err != nil {
		exitError("No registry to withdraw from: %v", err)
	}
	if r.Find(domain) == nil {
		exitError("%s is not in this operator's registry", domain)
	}

	privKey, err := loadPrivateKey(dataDir)
	if err != nil {
		exitError("Failed to load the operator's private key: %v", err)
	}
	defer signing.ZeroKey(privKey)

	kept := make([]actor.Entry, 0, len(r.Actors))
	for _, e := range r.Actors {
		if e.Domain != domain {
			kept = append(kept, e)
		}
	}
	r.Actors = kept
	r.Operator = operator
	r.Asserted = time.Now().UTC().Format(time.RFC3339)
	r.Generator = actor.GetGenerator()

	if err := actor.StateRegistry(dataDir, r, privKey); err != nil {
		exitError("Failed to write the registry: %v", err)
	}

	// ⛔ REMOVING AN ACTOR FROM THE FILE MUST ALSO ISSUE A WITHDRAWAL, or every
	// removal reads as drift forever: the permanent record would still carry a
	// live disclosure for an actor the file no longer lists, and a checker
	// comparing the two has no way to tell a retirement from a tampered file.
	withdrawn := 0
	if !noAttest {
		records, err := attestation.List(dataDir)
		if err != nil {
			exitError("Failed to read the disclosure records: %v", err)
		}
		for _, rec := range records {
			if rec.Predicate != attestation.PredicateAgentDisclosure {
				continue
			}
			if rec.Subject.ID != "https://"+domain {
				continue
			}
			if _, err := attestation.Withdraw(dataDir, attestation.ID(rec), privKey); err != nil {
				// One withdrawal per claim; a second is refused and that is not
				// a failure of this command.
				fmt.Fprintf(os.Stderr, "[!] %v\n", err)
				continue
			}
			withdrawn++
		}
	}

	announced := announceActorEvent("pub.polis.actor.withdrawn", domain, dataDir, privKey, noAnnounce)

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "actor",
			"data": map[string]interface{}{
				"operator":            operator,
				"domain":              domain,
				"remaining":           len(r.Actors),
				"disclosures_retired": withdrawn,
				"event":               "pub.polis.actor.withdrawn",
				"announced":           announced,
			},
		})
		return
	}
	fmt.Printf("[✓] Withdrew %s — %d actor(s) remain\n", domain, len(r.Actors))
	fmt.Printf("    %d disclosure record(s) retired; the records themselves stay where they are\n", withdrawn)
}

func handleActorList() {
	dataDir := getDataDir()
	r, err := actor.Load(actor.RegistryPath(dataDir))
	if err != nil {
		if jsonOutput {
			outputJSON(map[string]interface{}{
				"status": "success", "command": "actor",
				"data": map[string]interface{}{"actors": []interface{}{}, "count": 0},
			})
			return
		}
		fmt.Println("[i] This site publishes no actor registry")
		return
	}

	if jsonOutput {
		out := make([]map[string]interface{}, 0, len(r.Actors))
		for _, e := range r.Actors {
			out = append(out, map[string]interface{}{
				"domain":           e.Domain,
				"authority":        e.Authority,
				"expected_actions": e.ExpectedActions,
				"countersigned":    e.Countersignature != "",
			})
		}
		outputJSON(map[string]interface{}{
			"status": "success", "command": "actor",
			"data": map[string]interface{}{
				"operator": r.Operator,
				"asserted": r.Asserted,
				"actors":   out,
				"count":    len(out),
			},
		})
		return
	}

	fmt.Printf("Operator: %s  (asserted %s)\n", r.Operator, r.Asserted)
	if len(r.Actors) == 0 {
		fmt.Println("  (no actors listed)")
		return
	}
	for _, e := range r.Actors {
		mark := " "
		if e.Countersignature != "" {
			mark = "✓"
		}
		fmt.Printf("  %s %-28s %-9s %s\n", mark, e.Domain, e.Authority, strings.Join(e.ExpectedActions, ", "))
	}
	fmt.Println("\n  ✓ = countersigned by the actor. Expected actions are DESCRIPTIVE:")
	fmt.Println("      an action outside this list is information, not a blocked operation.")
}

func handleActorVerify(args []string) {
	tenantsDir, operator, action, custody, ds := "", "", "", "", ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--tenants-dir" && i+1 < len(args):
			tenantsDir = args[i+1]
			i++
		case args[i] == "--operator" && i+1 < len(args):
			operator = args[i+1]
			i++
		case args[i] == "--custody" && i+1 < len(args):
			custody = args[i+1]
			i++
		case args[i] == "--ds" && i+1 < len(args):
			ds = args[i+1]
			i++
		case strings.HasPrefix(args[i], "-"):
			exitError("unknown flag: %s", args[i])
		default:
			if action != "" {
				exitError("unexpected argument: %s", args[i])
			}
			action = args[i]
		}
	}

	// ⭐ Signet epic 17: --custody is a TENANT's check of her own custody records,
	// read-only and keyless, so it runs from her machine rather than from any
	// surface her operator hosts.
	if custody != "" {
		if action != "" || tenantsDir != "" {
			exitError("--custody checks one site's custody records; it does not combine with an action or --tenants-dir")
		}
		handleActorVerifyCustody(custody, operator, ds)
		return
	}
	if ds != "" {
		exitError("--ds is only used with --custody <domain>")
	}

	// ⭐ An argument selects the STRANGER's form (Signet epic 33 D8). Without one,
	// this is the operator checking its own registry off local disk.
	if action != "" {
		if tenantsDir != "" {
			exitError("--tenants-dir checks this operator's own registry; it does not combine with an action")
		}
		handleActorVerifyAction(action, operator)
		return
	}
	if operator != "" {
		exitError("--operator needs an action to check: polis actor verify <action-url-or-file> --operator <domain>")
	}

	dataDir := getDataDir()

	r, err := actor.Load(actor.RegistryPath(dataDir))
	if err != nil {
		exitError("No registry to verify: %v", err)
	}
	wk, err := site.LoadWellKnown(dataDir)
	if err != nil {
		exitError("Cannot read this site's published key: %v", err)
	}

	status, err := actor.Verify(r, []byte(wk.PublicKey))
	if err != nil && status != actor.StatusInvalid {
		exitError("Verify failed: %v", err)
	}

	type entryResult struct {
		Domain string `json:"domain"`
		Status string `json:"countersignature"`
	}
	var results []entryResult
	for i := range r.Actors {
		e := &r.Actors[i]
		var pub []byte
		if tenantsDir != "" && e.Domain != "" {
			// The actor's key comes from the ACTOR's own .well-known/polis —
			// never from the registry, which deliberately holds no keys so that
			// a rotation does not stale every entry.
			handle := strings.SplitN(e.Domain, ".", 2)[0]
			if awk, err := site.LoadWellKnown(tenantsDir + "/" + handle); err == nil {
				pub = []byte(awk.PublicKey)
			}
		}
		st, _ := actor.VerifyCountersignature(r, e, pub)
		results = append(results, entryResult{Domain: e.Domain, Status: string(st)})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Domain < results[j].Domain })

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status": "success", "command": "actor",
			"data": map[string]interface{}{
				"registry_signature": string(status),
				"entries":            results,
			},
		})
		return
	}
	fmt.Printf("Registry signature: %s\n", status)
	for _, e := range results {
		fmt.Printf("  %-28s countersignature: %s\n", e.Domain, e.Status)
	}
	if status != actor.StatusValid {
		os.Exit(1)
	}
}

// handleActorVerifyAction runs docs/signet/spec/custody.md §7 over one signed
// action, fetching everything else over HTTPS.
//
// ⛔ IT NEEDS NO SITE AND NO KEY. The whole point of §7 is that a stranger can
// run it, so nothing here reads the data directory.
//
// The action is read from a URL or a file. A file is not a lesser input: an
// action that was never published is exactly what a canary trip checks, and a
// record passed hand to hand is still signed.
//
// ⚠️ Only pub.polis.attestation records today. The registry's expected actions
// also name stream event types, and checking one of those needs the event's own
// signature verified — a separate reader nobody has built.
func handleActorVerifyAction(source, operator string) {
	operator = strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(operator, "https://"), "http://"), "/")
	if strings.Contains(operator, "/") {
		exitError("--operator names a DOMAIN, e.g. polis.polis.pub, got %q", operator)
	}

	client := remote.NewClient()
	var body []byte
	if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://") {
		s, err := client.FetchContent(source)
		if err != nil {
			exitError("Could not fetch the action: %v", err)
		}
		body = []byte(s)
	} else {
		b, err := os.ReadFile(source)
		if err != nil {
			exitError("Could not read the action: %v", err)
		}
		body = b
	}

	// ⭐ Parse, never json.Unmarshal: it keeps the members this build does not
	// model, which is what lets step 1 say "could not check" instead of calling
	// a newer record forged (SIGNET epic 47).
	rec, err := attestation.Parse(body)
	if err != nil {
		exitError("%s is not a JSON record: %v", source, err)
	}
	if rec.Type != attestation.TypeName {
		exitError("%s is a %q, and only %s records can be checked today", source, rec.Type, attestation.TypeName)
	}

	fetch := func(u string) ([]byte, error) {
		s, err := client.FetchContent(u)
		return []byte(s), err
	}
	result := actor.CheckAction(rec, source, operator, fetch)

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status": "success", "command": "actor",
			"data": result,
		})
	} else {
		printStrangerCheck(result)
	}
	if len(result.Findings) > 0 {
		os.Exit(1)
	}
}

// handleActorVerifyCustody runs the tenant's custody check (docs/signet/spec/
// custody.md §12) over HTTPS. ⛔ Like the stranger's check it reads no data
// directory and no key — it must run the same from a laptop that has never seen
// the site's files.
func handleActorVerifyCustody(siteDomain, operator, ds string) {
	if ds == "" {
		ds = discoveryURL
	}
	if ds == "" {
		ds = "https://ds.polis.pub"
	}
	client := remote.NewClient()
	fetch := func(u string) ([]byte, error) {
		s, err := client.FetchContent(u)
		return []byte(s), err
	}
	result := actor.CheckCustody(siteDomain, operator, ds, fetch)

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status": "success", "command": "actor",
			"data": result,
		})
	} else {
		printCustodyCheck(result)
	}
	if len(result.Findings) > 0 {
		os.Exit(1)
	}
}

// printCustodyCheck answers the three plain questions — never in the
// grant / disclosure / attribution vocabulary, which belongs in the spec.
func printCustodyCheck(c *actor.CustodyCheck) {
	mark := func(sig string) string {
		switch sig {
		case string(attestation.StatusValid):
			return "✓"
		case string(attestation.StatusInvalid):
			return "✗"
		}
		return "?"
	}
	withdrawn := func(w string) string {
		switch w {
		case actor.WithdrawalWithdrawn:
			return " — WITHDRAWN"
		case actor.WithdrawalUnknown:
			return " — could not tell whether it was withdrawn"
		}
		return ""
	}

	fmt.Printf("Custody of %s\n", c.Site)
	if c.Operator != "" {
		fmt.Printf("Operator:  %s (%s)\n", c.Operator, c.OperatorSource)
	}

	fmt.Println()
	fmt.Println("What does this helper do?  — your operator's signed declaration about you")
	if len(c.Declarations) == 0 {
		fmt.Println("  nothing declared")
	}
	for _, d := range c.Declarations {
		fmt.Printf("  [%s] %s  %s holds your %s%s\n", mark(d.Signature), d.Asserted, strings.TrimPrefix(d.Issuer, "https://"),
			strings.ReplaceAll(d.Payload[attestation.CustodyKeyHolds], "-", " "), withdrawn(d.Withdrawal))
		switch d.Payload[attestation.CustodyKeyAttribution] {
		case attestation.CustodyAttributionAsTenant:
			fmt.Println("      and says it signs AS YOU. Anything signed with your key may be yours or your")
			fmt.Println("      operator's — only you know which you did yourself.")
		case attestation.CustodyAttributionCoSigned:
			fmt.Println("      and says it CO-SIGNS what it does. Something signed only with your key that you")
			fmt.Println("      did not do yourself contradicts that.")
		default:
			fmt.Printf("      attribution: %q\n", d.Payload[attestation.CustodyKeyAttribution])
		}
		if d.KeyNote != "" {
			fmt.Printf("      %s\n", d.KeyNote)
		}
	}

	fmt.Println()
	fmt.Println("What did I allow?  — custody grants published on your site")
	if len(c.Grants) == 0 {
		fmt.Println("  none published")
	}
	for _, g := range c.Grants {
		basis := g.Payload[attestation.CustodyKeyBasis]
		switch basis {
		case attestation.CustodyBasisHostingTerms:
			basis = "issued under your operator's hosting terms — not signed by you in person"
		case attestation.CustodyBasisUserSigned:
			basis = "signed by you"
		}
		fmt.Printf("  [%s] %s  custody of your key to %s — %s%s\n", mark(g.Signature), g.Asserted,
			strings.TrimPrefix(g.Subject, "https://"), basis, withdrawn(g.Withdrawal))
		if g.KeyNote != "" {
			fmt.Printf("      %s\n", g.KeyNote)
		}
	}

	if ev := c.Events; ev != nil {
		fmt.Println()
		fmt.Printf("Who did this?  — your events at %s, as it recorded them\n", ev.Source)
		if ev.Error != "" {
			fmt.Printf("  could not read them: %s\n", ev.Error)
		} else {
			fmt.Printf("  %d event(s): %d signed with your key, %d signed by someone else, %d from before this was recorded\n",
				ev.Examined, ev.SignedWithYourKey, len(ev.SignedByOther), ev.NotRecorded)
			fmt.Printf("  of those signed with your key: %d by you, %d by an agent under a grant\n",
				ev.SignedByYou, ev.SignedByAgent)
			if ev.Truncated {
				fmt.Println("  (stopped early — there are more)")
			}
			for _, e := range ev.SignedByOther {
				fmt.Printf("  - #%s %s  signed by %s, authority: %s\n", e.ID, e.Type, e.SignedBy, e.Authority)
			}
			for _, a := range ev.AgentActs {
				state := ""
				if a.GrantState != "" {
					state = " (" + a.GrantState + ")"
				}
				fmt.Printf("  - #%s %s  signed with your key by agent %s, under grant %s%s\n", a.ID, a.Type, a.Agent, a.Grant, state)
			}
			if ev.SignedByAgent > len(ev.AgentActs) {
				fmt.Printf("  (%d more agent act(s) not listed)\n", ev.SignedByAgent-len(ev.AgentActs))
			}
		}
	}

	if len(c.Notes) > 0 {
		fmt.Println()
		for _, n := range c.Notes {
			fmt.Printf("[i] %s\n", n)
		}
	}
	fmt.Println()
	fmt.Printf("[i] %s\n", c.Limit)
	for _, f := range c.Findings {
		fmt.Printf("[!] %s — %s\n", strings.ToUpper(f.Kind), f.Detail)
	}
}

func printStrangerCheck(c *actor.StrangerCheck) {
	fmt.Printf("Action:   %s\n", c.Action.Type)
	fmt.Printf("Signer:   %s\n", c.Action.Signer)
	if c.Operator != "" {
		fmt.Printf("Operator: %s (%s)\n", c.Operator, c.OperatorSource)
	}
	fmt.Println()
	marks := map[actor.StepOutcome]string{
		actor.StepPassed: "✓", actor.StepFailed: "✗", actor.StepWeaker: "~",
		actor.StepUnknown: "?", actor.StepNotApplicable: "-", actor.StepNotReached: " ",
	}
	for _, s := range c.Steps {
		detail := s.Detail
		if s.Outcome == actor.StepNotReached {
			detail = "not reached"
		}
		fmt.Printf("  [%s] %d %-17s %s\n", marks[s.Outcome], s.Step, s.Name, detail)
	}
	fmt.Println()
	if len(c.Findings) == 0 {
		fmt.Println("[i] No findings.")
		return
	}
	for _, f := range c.Findings {
		fmt.Printf("[!] %s (step %d)\n", strings.ToUpper(f.Kind), f.Step)
	}
}

// announceActorEvent publishes the stream event and reports whether it landed.
//
// ⛔ THE `actor` COLUMN IS THE OPERATOR, ALWAYS — never the actor being
// registered — because the claim is the operator's. stream.PublishEvent derives
// it from POLIS_BASE_URL, which is the operator's own site, so this is correct
// by construction rather than by convention. The obvious later "fix" of putting
// the registered actor there is a CORRECTNESS bug, not a tidy-up: the DS
// verifies the signature against the named domain's published key, so such an
// event would be rejected 401.
//
// ⚠️ Non-fatal, the same as tag and attestation registration: a registry that
// exists and is not yet announced is a normal, recoverable state, and failing
// the command would leave a signed file the user believes was never written.
func announceActorEvent(eventType, targetDomain, dataDir string, privKey []byte, skip bool) bool {
	if skip {
		return false
	}
	if discoveryURL == "" || baseURL == "" {
		fmt.Fprintf(os.Stderr, "[i] Announcement skipped: DISCOVERY_SERVICE_URL or POLIS_BASE_URL not set\n")
		return false
	}
	payload := map[string]interface{}{
		// The SUBJECT of the claim. Indexed by the DS, and the counterpart to
		// the actor column above.
		"target_domain": targetDomain,
		// ⭐ A POINTER, NOT THE FACTS — mirroring site.registered. The event
		// says "go look"; the file says what is true. Putting the entry's
		// contents here would invite a consumer to trust a 90-day-lived record
		// over the canonical one.
		"registry_url": registryURL(polisurl.ExtractDomain(baseURL), dataDir),
	}
	cfg := &stream.DiscoveryConfig{
		DiscoveryURL: discoveryURL,
		DiscoveryKey: discoveryKey,
		BaseURL:      baseURL,
		DataDir:      dataDir,
	}
	if err := stream.PublishEvent(eventType, payload, privKey, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[!] Announcement skipped: %v\n", err)
		return false
	}
	return true
}

func registryURL(operatorDomain, dataDir string) string {
	return "https://" + operatorDomain + actor.RegistryURLPath(dataDir)
}

// upsert replaces an entry with the same domain, or appends. Order is otherwise
// preserved: the signature covers the list as it was written, so re-ordering it
// would change the bytes for no reason.
func upsert(entries []actor.Entry, e actor.Entry) []actor.Entry {
	for i := range entries {
		if entries[i].Domain == e.Domain {
			entries[i] = e
			return entries
		}
	}
	return append(entries, e)
}

// handleActorDeclareOperator is run from an ACTOR's own site and names the
// party accountable for it.
//
// ⛔ IT IS A CLAIM, NOT A FACT, and nothing here pretends otherwise. A site can
// name any operator it likes; what makes the claim mean something is the named
// operator's registry listing this domain back. The check is always in that
// direction, because the operator is the one with something to lose.
//
// It exists as a separate subcommand because an actor may not live on the same
// machine as its operator. When it does, `register --countersign-with` writes
// this pointer for you.
func handleActorDeclareOperator(args []string) {
	operator := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			exitError("unknown flag: %s", a)
		}
		operator = a
	}
	if operator == "" {
		exitError("Usage: polis actor declare-operator <operator-domain>")
	}
	if strings.Contains(operator, "/") || strings.Contains(operator, ":") {
		exitError("%q looks like a URL — name the operator's site ORIGIN as a domain", operator)
	}

	dataDir := getDataDir()
	if err := site.SetOperatorPointer(dataDir, operator); err != nil {
		exitError("Failed to write the operator pointer: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status": "success", "command": "actor",
			"data": map[string]interface{}{"operator": operator},
		})
		return
	}
	fmt.Printf("[\u2713] This site declares %s as its operator\n", operator)
	fmt.Printf("    It is a CLAIM. A reader corroborates it by fetching that operator's\n")
	fmt.Printf("    registry and checking this domain is listed.\n")
}

// handleActorAnnounce publishes pub.polis.actor.registered for an actor that
// is ALREADY in the registry, and does nothing else.
//
// ⭐ IT EXISTS SO THE ANNOUNCEMENT CAN GO LAST. The stream event is the one
// artifact that cannot be unsaid — the stream is append-only — so the order of
// work puts it after the file and the attestations are verified over HTTPS.
// `register --no-announce` followed by a second `register` would get there the
// wrong way: it writes a SECOND permanent disclosure record for the same actor,
// and it announces `reregistered` where the first announcement means
// `registered`.
func handleActorAnnounce(args []string) {
	domain := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			exitError("unknown flag: %s", a)
		}
		domain = a
	}
	if domain == "" {
		exitError("Usage: polis actor announce <domain>")
	}

	dataDir := getDataDir()
	operator := operatorIdentity()
	if err := checkAnnounceable(dataDir, domain); err != nil {
		exitError("%v", err)
	}

	privKey, err := loadPrivateKey(dataDir)
	if err != nil {
		exitError("Failed to load the operator's private key: %v", err)
	}
	defer signing.ZeroKey(privKey)

	const eventType = "pub.polis.actor.registered"
	if !announceActorEvent(eventType, domain, dataDir, privKey, false) {
		// Unlike register, announcing IS this command's whole job.
		exitError("the announcement was not published — see the message above")
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status": "success", "command": "actor",
			"data": map[string]interface{}{
				"operator":     operator,
				"domain":       domain,
				"event":        eventType,
				"announced":    true,
				"registry_url": registryURL(operator, dataDir),
			},
		})
		return
	}
	fmt.Printf("[\u2713] Announced %s to the network (%s)\n", domain, eventType)
	fmt.Printf("    The event points at the registry; the registry is what is authoritative.\n")
}

// checkAnnounceable refuses to announce anything the registry would not back.
//
// ⛔ The announcement points readers at the file, so announcing an actor the
// file does not list — or a file that does not verify against this site's own
// published key — would send the network to look at something that
// contradicts the announcement. The event cannot be withdrawn; the check has
// to come first.
func checkAnnounceable(dataDir, domain string) error {
	r, err := actor.Load(actor.RegistryPath(dataDir))
	if err != nil {
		return fmt.Errorf("no registry to announce from: %w", err)
	}
	if r.Find(domain) == nil {
		return fmt.Errorf("%s is not in this operator's registry — register it first", domain)
	}
	wk, err := site.LoadWellKnown(dataDir)
	if err != nil || wk.PublicKey == "" {
		return fmt.Errorf("cannot read this site's published key, so the registry cannot be checked before announcing")
	}
	status, _ := actor.Verify(r, []byte(wk.PublicKey))
	if status != actor.StatusValid {
		return fmt.Errorf("the registry is %s against this site's published key — refusing to announce it", status)
	}
	if site.ActorRegistryPointer(dataDir) == "" {
		return fmt.Errorf(".well-known/polis publishes no actor_registry pointer — a reader following the announcement could not find the file")
	}
	return nil
}
