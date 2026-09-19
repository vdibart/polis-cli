package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// handleAttest dispatches `polis attest`.
//
// ⚠️ There is deliberately NO delete or remove subcommand, and its absence is a
// design decision rather than an omission. Withdrawing a claim is a signed
// revocation record, never a file deletion: a 404 must never be readable as a
// retraction, because that teaches the network to treat an outage, a moved
// site, or a lapsed domain as a retraction too — and the evidence is genuinely
// gone. Unreachability is evidence for DURABILITY (keep serving the cache),
// never for eviction.
func handleAttest(args []string) {
	if len(args) < 1 {
		printAttestUsage()
		os.Exit(1)
	}

	sub, subArgs := args[0], args[1:]
	switch sub {
	case "issue":
		handleAttestIssue(subArgs)
	case "list":
		handleAttestList()
	case "show":
		if len(subArgs) < 1 {
			exitError("Usage: polis attest show <id>")
		}
		handleAttestShow(subArgs[0])
	case "verify":
		handleAttestVerify(subArgs)
	case "withdraw":
		if len(subArgs) < 1 {
			exitError("Usage: polis attest withdraw <id>")
		}
		handleAttestWithdraw(subArgs[0])
	case "register":
		if len(subArgs) < 1 {
			exitError("Usage: polis attest register <id>")
		}
		handleAttestRegister(subArgs[0])
	default:
		fmt.Fprintf(os.Stderr, "Unknown attest subcommand: %s\n", sub)
		printAttestUsage()
		os.Exit(1)
	}
}

func printAttestUsage() {
	fmt.Print(`Polis Attest - Signed claims about other people and works

An attestation is a claim event: on this date, you asserted this predicate
about this subject. One claim, one file, one signature.

Usage:
  polis attest <subcommand> [options]

Subcommands:
  issue     Issue a new attestation
  list      List attestations this site has issued
  show      Show one attestation
  verify    Verify issued attestations against this site's published key
  withdraw  Retract a claim you issued — adds a record, deletes nothing
  register  Announce a record you already issued to the discovery service

Issue options:
  --predicate <p>       What is being asserted. FULLY QUALIFIED, always
  --subject <id>        What the claim is about (an https URL)
  --subject-type <t>    "identity" (a party) or "uri" (a work). Default: uri
  --subject-version <h> Pin a uri subject to exact bytes: sha256:<64 hex>
  --payload k=v         Predicate-specific detail. Repeatable
  --asserted <ts>       RFC 3339, e.g. 2026-08-28T00:00:00Z. Default: now

Reserved predicates (short names are for prose; files use the full form):
  pub.polis.attestation.same-as           this identity and that one are the same party
  pub.polis.attestation.integrity         an integrity OBSERVATION of this site; the finding is
                                          in the payload — requires result=verified|not-verified,
                                          vantage=… and observed=<RFC 3339>, no default
  pub.polis.attestation.correction        this version of this work is corrected
  pub.polis.attestation.used-under-terms  I used this work, under these terms
  pub.polis.attestation.agent-disclosure  this identity is an automated agent
  pub.polis.attestation.endorsement       I vouch for this party
  pub.polis.attestation.withdrawal        the record at this URL is retracted by its issuer
                                          (issued ONLY by "polis attest withdraw" — "issue" refuses it)
  pub.polis.attestation.custody           an operator holds this party's key — requires
                                          holds=identity-key and attribution=as-tenant|co-signed
  pub.polis.attestation.custody-grant     you grant custody of your key to this operator — requires
                                          scope=custodial and basis=user-signed|hosting-terms

Custody records make custody VISIBLE. They do not reduce what a key holder can do.

The list is RESERVED, not a registry: a predicate outside it is legal, and a
reader that does not recognise one must still verify and render the record.
A third party's predicate looks like com.example.reviewed.

Examples:
  # Identity continuity — the other half is issued from the other site
  polis attest issue --predicate pub.polis.attestation.same-as \
    --subject https://vincent.example.com --subject-type identity

  # A correction pinned to exact bytes, so an edit cannot move it
  polis attest issue --predicate pub.polis.attestation.correction \
    --subject https://site.example/posts/20260901-claim.md \
    --subject-version sha256:9f2a... \
    --payload note="the figure cited was revised by the source"

  polis attest list
  polis attest show 20260828T140200Z-3f2a9c1d4e5b6a70
  polis attest verify
  polis attest withdraw 20260828T140200Z-3f2a9c1d4e5b6a70
  polis attest register 20260828T140200Z-3f2a9c1d4e5b6a70
`)
}

// attestIssueFlags is the parsed form of `polis attest issue`.
type attestIssueFlags struct {
	predicate      string
	subject        string
	subjectType    string
	subjectVersion string
	asserted       string
	payload        map[string]string
}

func parseAttestIssueFlags(args []string) (*attestIssueFlags, error) {
	f := &attestIssueFlags{
		subjectType: attestation.SubjectURI,
		payload:     map[string]string{},
	}
	for i := 0; i < len(args); i++ {
		next := func(name string) (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		switch args[i] {
		case "--predicate":
			f.predicate, err = next("--predicate")
		case "--subject":
			f.subject, err = next("--subject")
		case "--subject-type":
			f.subjectType, err = next("--subject-type")
		case "--subject-version":
			f.subjectVersion, err = next("--subject-version")
		case "--asserted":
			f.asserted, err = next("--asserted")
		case "--payload":
			var kv string
			kv, err = next("--payload")
			if err == nil {
				k, v, found := strings.Cut(kv, "=")
				if !found || strings.TrimSpace(k) == "" {
					err = fmt.Errorf("--payload expects key=value, got %q", kv)
				} else {
					f.payload[k] = v
				}
			}
		default:
			err = fmt.Errorf("unknown option: %s", args[i])
		}
		if err != nil {
			return nil, err
		}
	}
	if f.predicate == "" {
		return nil, fmt.Errorf("--predicate is required")
	}
	if f.subject == "" {
		return nil, fmt.Errorf("--subject is required")
	}
	return f, nil
}

func handleAttestIssue(args []string) {
	f, err := parseAttestIssueFlags(args)
	if err != nil {
		exitError("%v", err)
	}

	dataDir := getDataDir()

	// The issuer is this site. A claim has to say who made it, and the key that
	// signs the record is the one published at that URL — so an issuer we
	// cannot name is a record nobody can verify.
	issuer := strings.TrimRight(baseURL, "/")
	if issuer == "" {
		exitError("POLIS_BASE_URL is not set — an attestation must name the site that issued it")
	}

	privKey, err := loadPrivateKey(dataDir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}
	defer signing.ZeroKey(privKey)

	rec := &attestation.Record{
		Issuer:    issuer,
		Predicate: f.predicate,
		Subject: attestation.Subject{
			Type:    f.subjectType,
			ID:      f.subject,
			Version: f.subjectVersion,
		},
		Payload:  f.payload,
		Asserted: f.asserted,
	}

	id, err := attestation.Issue(dataDir, rec, privKey)
	if err != nil {
		exitError("Failed to issue attestation: %v", err)
	}

	// DS registration is non-fatal, the same as tag and comment: a record that
	// exists on disk and is not yet announced is a normal, recoverable state,
	// whereas failing the command would leave a signed record the user believes
	// was never written.
	if discoveryURL != "" && baseURL != "" {
		cfg := &attestation.DiscoveryConfig{
			DiscoveryURL: discoveryURL,
			DiscoveryKey: discoveryKey,
			BaseURL:      baseURL,
			DataDir:      dataDir,
		}
		if err := attestation.Register(rec, id, privKey, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "[!] Discovery registration skipped: %v\n", err)
		}
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "attest",
			"data": map[string]interface{}{
				"id":              id,
				"url":             attestation.RecordURL(issuer, id),
				"issuer":          rec.Issuer,
				"predicate":       rec.Predicate,
				"subject":         rec.Subject.ID,
				"subject_type":    rec.Subject.Type,
				"subject_version": rec.Subject.Version,
				"asserted":        rec.Asserted,
				"current_version": rec.Version,
			},
		})
		return
	}

	fmt.Printf("[✓] Attested %s about %s\n", rec.Predicate, rec.Subject.ID)
	fmt.Printf("    %s\n", attestation.RecordURL(issuer, id))
}

func handleAttestList() {
	dataDir := getDataDir()

	records, err := attestation.List(dataDir)
	if err != nil {
		exitError("Failed to list attestations: %v", err)
	}

	if jsonOutput {
		out := make([]map[string]interface{}, 0, len(records))
		for _, r := range records {
			out = append(out, map[string]interface{}{
				"id":        attestation.ID(r),
				"predicate": r.Predicate,
				"subject":   r.Subject.ID,
				"asserted":  r.Asserted,
			})
		}
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "attest",
			"data": map[string]interface{}{
				"attestations": out,
				"count":        len(out),
			},
		})
		return
	}

	if len(records) == 0 {
		fmt.Println("[i] No attestations issued")
		return
	}
	for _, r := range records {
		fmt.Printf("  %s  %s → %s\n", attestation.ID(r), r.Predicate, r.Subject.ID)
	}
}

func handleAttestShow(id string) {
	dataDir := getDataDir()

	r, err := attestation.Load(attestation.Path(dataDir, id))
	if err != nil {
		exitError("Failed to load attestation %q: %v", id, err)
	}
	v := newAttestVerifier(dataDir)
	status, used, verr := v.verify(r)

	if jsonOutput {
		data := map[string]interface{}{
			"id":               attestation.ID(r),
			"issuer":           r.Issuer,
			"predicate":        r.Predicate,
			"subject":          r.Subject.ID,
			"subject_type":     r.Subject.Type,
			"subject_version":  r.Subject.Version,
			"payload":          r.Payload,
			"asserted":         r.Asserted,
			"generator":        r.Generator,
			"current_version":  r.Version,
			"signature_status": string(status),
		}
		// Discovery, not proof: the withdrawal it names is the signed record.
		if r.WithdrawnBy != "" {
			data["withdrawn_by"] = r.WithdrawnBy
		}
		if used != nil {
			data["signature_key"] = used
		}
		if verr != nil {
			data["signature_detail"] = verr.Error()
		}
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "attest",
			"data":    data,
		})
		return
	}

	fmt.Printf("[i] %s\n", attestation.ID(r))
	fmt.Printf("    issuer:    %s\n", r.Issuer)
	fmt.Printf("    predicate: %s\n", r.Predicate)
	fmt.Printf("    subject:   %s (%s)\n", r.Subject.ID, r.Subject.Type)
	if r.Subject.Version != "" {
		fmt.Printf("    pinned at: %s\n", r.Subject.Version)
	}
	for k, v := range r.Payload {
		fmt.Printf("    %s: %s\n", k, v)
	}
	fmt.Printf("    asserted:  %s\n", r.Asserted)
	if r.WithdrawnBy != "" {
		fmt.Printf("    withdrawn: %s (pointer only — verify the withdrawal it names)\n", r.WithdrawnBy)
	}
	fmt.Printf("    signature: %s\n", status)
	if d := used.Describe(); d != "" {
		fmt.Printf("               %s\n", d)
	}
	if verr != nil {
		fmt.Printf("               %v\n", verr)
	}
}

// handleAttestVerify checks issued records against the site's PUBLISHED key —
// the key a third party on the network would fetch, so this answers the same
// question everyone else is asking.
//
// ⚠️ It reports a status and never a verdict. `unsigned` is a fact, `unknown`
// means we could not look, and only `invalid` is a finding. Collapsing any two
// of those makes a judgment that belongs to whoever is asking. The exit code
// follows the same rule: it is non-zero only for `invalid`.
//
// ⛔ SIGNET epic 44 (C4): a record signed before a key rotation is checked
// against the key the site's published history says was current at its
// `asserted` time — the same predicate `polis validate` uses — so the two
// commands never disagree about the same record. A retired-key pass is named.
func handleAttestVerify(args []string) {
	dataDir := getDataDir()
	v := newAttestVerifier(dataDir)

	if len(args) > 0 {
		id := args[0]
		r, err := attestation.Load(attestation.Path(dataDir, id))
		if err != nil {
			reportAttestVerify(map[string]attestation.SignatureStatus{id: attestation.StatusUnknown}, nil, err)
			return
		}
		status, used, verr := v.verify(r)
		reportAttestVerify(map[string]attestation.SignatureStatus{id: status}, retiredNames(id, used), verr)
		return
	}

	records, err := attestation.List(dataDir)
	if err != nil {
		exitError("Failed to verify attestations: %v", err)
	}
	statuses := make(map[string]attestation.SignatureStatus, len(records))
	retired := map[string]string{}
	for _, r := range records {
		id := attestation.ID(r)
		status, used, _ := v.verify(r)
		statuses[id] = status
		for k, d := range retiredNames(id, used) {
			retired[k] = d
		}
	}
	reportAttestVerify(statuses, retired, nil)
}

// attestVerifier holds a site's published key and the key history it may
// resolve retired keys from, read once per command.
type attestVerifier struct {
	pubKey []byte
	keyErr error
	chain  *site.KeyHistoryBlock
}

// newAttestVerifier reads the site's PUBLISHED key — the one a third party
// fetches — and its usable key history. The domain inside every transition
// signature comes from POLIS_BASE_URL when set, else the site's own did.json
// (so a clone of someone else's site resolves too).
func newAttestVerifier(dataDir string) *attestVerifier {
	pub, err := sitecheck.PublishedKey(dataDir)
	if err != nil {
		return &attestVerifier{keyErr: err}
	}
	domain := ""
	if base := os.Getenv("POLIS_BASE_URL"); base != "" {
		domain = polisurl.ExtractDomain(base)
	}
	chain, _ := sitecheck.SiteResolvingChain(dataDir, domain, string(pub))
	return &attestVerifier{pubKey: pub, chain: chain}
}

func (v *attestVerifier) verify(r *attestation.Record) (attestation.SignatureStatus, *sitecheck.KeyUsed, error) {
	if r == nil || r.Signature == "" {
		return attestation.StatusUnsigned, nil, nil
	}
	if v.keyErr != nil {
		return attestation.StatusUnknown, nil, v.keyErr
	}
	status, retired, err := attestation.VerifyWithHistory(r, v.pubKey, v.chain)
	if status != attestation.StatusValid {
		return status, nil, err
	}
	return status, sitecheck.KeyUsedFor(v.chain, retired, r.Asserted), nil
}

// retiredNames is id → the sentence, when a retired key verified the record.
func retiredNames(id string, used *sitecheck.KeyUsed) map[string]string {
	if d := used.Describe(); d != "" {
		return map[string]string{id: d}
	}
	return nil
}

func reportAttestVerify(statuses map[string]attestation.SignatureStatus, retired map[string]string, detail error) {
	counts := map[string]int{}
	invalid := 0
	for _, st := range statuses {
		counts[string(st)]++
		if st == attestation.StatusInvalid {
			invalid++
		}
	}

	if jsonOutput {
		records := make(map[string]string, len(statuses))
		for id, st := range statuses {
			records[id] = string(st)
		}
		data := map[string]interface{}{
			"records": records,
			"counts":  counts,
			"invalid": invalid,
		}
		if len(retired) > 0 {
			data["retired"] = retired
		}
		if detail != nil {
			data["detail"] = detail.Error()
		}
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "attest",
			"data":    data,
		})
		if invalid > 0 {
			os.Exit(1)
		}
		return
	}

	if len(statuses) == 0 {
		fmt.Println("[i] No attestations issued")
		return
	}
	for id, st := range statuses {
		marker := "✓"
		if st == attestation.StatusInvalid {
			marker = "✗"
		} else if st != attestation.StatusValid {
			marker = "·"
		}
		fmt.Printf("  [%s] %s  %s\n", marker, id, st)
		if d := retired[id]; d != "" {
			fmt.Printf("      %s\n", d)
		}
	}
	if detail != nil {
		fmt.Printf("      %v\n", detail)
	}
	if invalid > 0 {
		fmt.Fprintf(os.Stderr, "[!] %d attestation(s) do not verify\n", invalid)
		os.Exit(1)
	}
}

// handleAttestWithdraw retracts a claim this site issued.
//
// ⚠️ It writes a NEW record and removes nothing. The retracted claim stays
// served, exactly where it was — a reader that fetches it still gets it, and
// learns it was withdrawn by finding the withdrawal. That is deliberate:
// unreachability is evidence for durability, so a 404 must never be the way a
// retraction is communicated. See epic 03 D6.
func handleAttestWithdraw(id string) {
	dataDir := getDataDir()
	if !isPolisSite(dataDir) {
		exitError("Not a polis site directory (no .well-known/polis found)")
	}

	privKey, err := loadPrivateKey(dataDir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}
	defer signing.ZeroKey(privKey)

	target, err := attestation.Load(attestation.Path(dataDir, id))
	if err != nil {
		exitError("No attestation %q to withdraw: %v", id, err)
	}

	newID, err := attestation.Withdraw(dataDir, id, privKey)
	if err != nil {
		exitError("Failed to withdraw: %v", err)
	}

	rec, err := attestation.Load(attestation.Path(dataDir, newID))
	if err != nil {
		exitError("Failed to read back the withdrawal: %v", err)
	}

	// Non-fatal, as for issue: a withdrawal on disk that the DS has not yet
	// heard about is recoverable, whereas failing here would leave the user
	// believing nothing was retracted when the signed record already exists.
	if discoveryURL != "" && baseURL != "" {
		cfg := &attestation.DiscoveryConfig{
			DiscoveryURL: discoveryURL,
			DiscoveryKey: discoveryKey,
			BaseURL:      baseURL,
			DataDir:      dataDir,
		}
		if err := attestation.Register(rec, newID, privKey, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "[!] Discovery registration skipped: %v\n", err)
		}
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "attest",
			"data": map[string]interface{}{
				"id":               newID,
				"url":              attestation.RecordURL(rec.Issuer, newID),
				"predicate":        rec.Predicate,
				"withdrew":         id,
				"withdrew_url":     rec.Subject.ID,
				"withdrew_version": rec.Subject.Version,
				"asserted":         rec.Asserted,
				"current_version":  rec.Version,
			},
		})
		return
	}

	fmt.Printf("[✓] Withdrew %s\n", id)
	fmt.Printf("    was: %s about %s\n", target.Predicate, target.Subject.ID)
	fmt.Printf("    withdrawal: %s\n", attestation.RecordURL(rec.Issuer, newID))
	fmt.Println()
	fmt.Println("    The withdrawn claim is still published and still verifies.")
	fmt.Println("    Retraction is a record, not a deletion — readers learn it was")
	fmt.Println("    withdrawn by finding this one, not by getting a 404.")
}

// handleAttestRegister announces a record this site already issued. `issue`
// registers as it writes; this is for a record written without the DS —
// `polis actor register`'s agent disclosures — or one whose registration at
// issue time was skipped. It refuses loudly where issue-time registration is
// quiet: asked to do nothing else, silence would read as success.
func handleAttestRegister(id string) {
	dataDir := getDataDir()
	privKey, err := loadPrivateKey(dataDir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}
	defer signing.ZeroKey(privKey)

	rec, err := attestation.RegisterIssued(dataDir, id, privKey, &attestation.DiscoveryConfig{
		DiscoveryURL: discoveryURL,
		DiscoveryKey: discoveryKey,
		BaseURL:      baseURL,
		DataDir:      dataDir,
	})
	if err != nil {
		exitError("Not registered: %v", err)
	}
	url := attestation.RecordURL(strings.TrimRight(baseURL, "/"), id)
	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "attest",
			"data": map[string]interface{}{
				"id":              id,
				"url":             url,
				"predicate":       rec.Predicate,
				"current_version": rec.Version,
				"registered_with": discoveryURL,
			},
		})
		return
	}
	fmt.Printf("[✓] Registered %s with %s\n", rec.Predicate, discoveryURL)
	fmt.Printf("    %s\n", url)
}
