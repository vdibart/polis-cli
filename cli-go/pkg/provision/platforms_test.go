package provision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐⭐ THE THREE-COLUMN CHECK, AS A TEST RATHER THAN PROSE.
//
// The risk this seam had to avoid was generalising ahead of evidence: there is
// exactly ONE implementation on the MVP path, so an interface designed
// abstractly and then bent to fit would have been the likely outcome. The
// discipline was to derive the operations from Vercel's shape and sanity-check
// each one against Fly and a VPS.
//
// ⛔ A SANITY CHECK THAT LIVES ONLY IN A TICKET COMMENT ROTS. So the table is
// here, and the test asserts the interface and the table have not drifted apart
// — which is what would happen if someone added an operation without asking
// whether the other two columns could implement it.
//
// ⚠️ Fly and VPS are NOT implemented, deliberately. The columns record that
// each operation is *meaningful* there, which is the claim the interface rests
// on; building them without a user is the generalisation this avoided.
type platformStep struct {
	vercel string
	fly    string
	vps    string
}

var operationTable = map[string]platformStep{
	"Create": {
		// ⭐ On Vercel an instance is the project AND its one private store,
		// so Create makes both (or adopts its own, on a re-run).
		vercel: "project create + its private Blob store",
		fly:    "tenant dir + row",
		// ⭐ "the box already exists" is why Instance has an Existing field:
		// adopting is a normal outcome, not a conflict.
		vps: "the box already exists — Create records it",
	},
	"PutCode": {
		// ⛔ NO GIT is a finding, not a preference: the A0 spike proved a
		// deployment can be uploaded directly.
		vercel: "upload a prebuilt deployment by digest, no git",
		fly:    "already running",
		vps:    "rsync / pull",
	},
	"Seed": {
		// ⭐ Straight into the private store, never through a setting or a
		// static file: a setting is too small and outlives its deletion in
		// older deployments, and static files are public.
		vercel: "write to the project's private Blob store with the owner's credential",
		fly:    "write the tenant directory",
		vps:    "copy into the site directory, at the site's file modes",
	},
	"SetConfig": {
		vercel: "env API, type: sensitive (write-only)",
		fly:    "fly secrets",
		// ⚠️ A platform that cannot distinguish a secret must REFUSE rather
		// than store one in the clear — which is why Config.Secret names keys
		// instead of leaving the platform to guess.
		vps: "a file, mode 0600",
	},
	"AttachDomain": {
		vercel: "domains API + managed cert",
		// ⚠️ Fly is where NeedsDNS earns its place: every custom hostname on
		// polis.pub needs its own _acme-challenge CNAME in Cloudflare, and a
		// missing one makes renewal fail SILENTLY months later.
		fly: "fly certs + Cloudflare DNS",
		vps: "DNS + certbot",
	},
	"Activate": {
		vercel: "wait for READY, then promote a deployment",
		fly:    "restart",
		vps:    "restart the unit",
	},
	"Status": {
		vercel: "API",
		fly:    "API",
		vps:    "ssh",
	},
	"Destroy": {
		vercel: "API",
		fly:    "Reaper",
		vps:    "ssh",
	},
}

// ⛔ EVERY INTERFACE METHOD MUST HAVE A ROW, AND EVERY ROW A METHOD.
//
// ⚠️ An operation with no row was added by symmetry rather than by evidence,
// which is exactly the failure this ticket named. A row with no method is a
// step the interface has stopped covering.
func TestEveryOperationIsJustifiedInAllThreeColumns(t *testing.T) {
	iface := reflect.TypeOf((*Provisioner)(nil)).Elem()

	var methods []string
	for i := 0; i < iface.NumMethod(); i++ {
		name := iface.Method(i).Name
		if name == "Name" {
			continue // identity, not an operation
		}
		methods = append(methods, name)
	}
	sort.Strings(methods)

	for _, m := range methods {
		step, ok := operationTable[m]
		if !ok {
			t.Errorf("%s is in the interface with no entry in the platform table. An "+
				"operation added by symmetry rather than by an actual provisioning step "+
				"does not belong in this seam — add the three columns, or remove it.", m)
			continue
		}
		// ⛔ A blank column means that platform could not implement it, which
		// means it should not be in the interface.
		if step.vercel == "" || step.fly == "" || step.vps == "" {
			t.Errorf("%s has an empty column (vercel=%q fly=%q vps=%q). An operation only "+
				"one backend could ever implement does not belong in the interface.",
				m, step.vercel, step.fly, step.vps)
		}
	}
	for row := range operationTable {
		found := false
		for _, m := range methods {
			if m == row {
				found = true
			}
		}
		if !found {
			t.Errorf("the table has a row for %s but the interface has no such method — a "+
				"provisioning step the seam has stopped covering", row)
		}
	}
	if len(methods) != 8 {
		t.Errorf("%d operations; the six provisioning steps (Seed among them) plus Status and Destroy is 8. "+
			"If that changed, the table and this count change with it.", len(methods))
	}
}

// ⛔⛔ NOTHING IN THE INTERFACE MAY REQUIRE A GIT REPOSITORY.
//
// ⭐ "No git" is a FINDING: the A0 spike proved a deployment can be uploaded
// directly, which removes a whole class of repository plumbing from the MVP and
// means a user needs no GitHub account. ⚠️ The way this regresses is someone
// adding a RepoURL field because a platform's happy path wants one.
func TestNothingInTheSeamRequiresGit(t *testing.T) {
	forbidden := []string{"git", "Git", "repo", "Repo", "github", "GitHub", "clone", "branch"}
	for _, typ := range []reflect.Type{
		reflect.TypeOf(CreateRequest{}),
		reflect.TypeOf(Code{}),
		reflect.TypeOf(Config{}),
		reflect.TypeOf(Instance{}),
		reflect.TypeOf(Release{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			for _, bad := range forbidden {
				if strings.Contains(f.Name, bad) {
					t.Errorf("%s.%s mentions %q — the seam must not require a repository. "+
						"A platform that wants one can clone it itself; a caller must not "+
						"need to have one.", typ.Name(), f.Name, bad)
				}
			}
		}
	}
	// ⭐ And Code must carry BYTES, because that is what "no git" means in
	// practice: the caller hands over a build, not a location to fetch one.
	code := reflect.TypeOf(Code{})
	if f, ok := code.FieldByName("Archive"); !ok || f.Type.Kind() != reflect.Slice {
		t.Error("Code.Archive must be bytes: uploading a build directly is what makes the " +
			"no-git path possible")
	}
}

// ⛔ ONLY THE VERCEL IMPLEMENTATION MAY KNOW ABOUT VERCEL.
//
// ⚠️ A seam leaks by a platform's vocabulary appearing above it. This checks
// the interface file itself, because that is where a leak would be load-bearing
// — a Vercel concept in the interface forces every other platform to pretend.
func TestOnlyTheImplementationKnowsAboutVercel(t *testing.T) {
	// ⭐ Seeding's order and refusals (seed.go) and its flow (seeded.go) are
	// above every platform too, so they are held to the same rule.
	var src string
	for _, f := range []string{"provision.go", "seed.go", "seeded.go", "platform.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src += string(b) + "\n"
	}
	// ⭐ Mentions in COMMENTS are fine and often necessary — the derivation is
	// from Vercel's shape and saying so is the point. What must not appear is
	// Vercel in the CODE.
	var code []string
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		code = append(code, line)
	}
	joined := strings.Join(code, "\n")
	for _, bad := range []string{"vercel", "Vercel", "VERCEL"} {
		if strings.Contains(joined, bad) {
			t.Errorf("the interface's CODE mentions %q. A platform concept in the interface "+
				"forces every other platform to pretend — keep it in the implementation.", bad)
		}
	}
	// ⛔ And the implementation must exist in its own file, so the boundary is
	// visible in the directory listing rather than only in the types.
	if _, err := os.Stat(filepath.Join(".", "vercel.go")); err != nil {
		t.Errorf("vercel.go is missing: %v", err)
	}
}

// fakePlatform is a Provisioner that records each call, in order, and the
// configuration it was given, so a flow's ordering can be asserted. Its store
// is in memory; Seed writes through SeedStore like every platform.
type fakePlatform struct {
	calls        []string
	config       Config
	store        storage.Store
	failSeed     bool
	failActivate bool
	destroyed    bool
}

func (f *fakePlatform) Name() string { return "fake" }
func (f *fakePlatform) Create(ctx context.Context, req CreateRequest) (Instance, error) {
	f.calls = append(f.calls, "Create")
	f.store = storage.NewObjectStore("")
	return Instance{ID: "fake-1", Name: req.Name}, nil
}
func (f *fakePlatform) PutCode(ctx context.Context, inst Instance, code Code) (Release, error) {
	f.calls = append(f.calls, "PutCode")
	return Release{ID: "rel-1"}, nil
}
func (f *fakePlatform) Seed(ctx context.Context, inst Instance, s storage.Site) error {
	f.calls = append(f.calls, "Seed")
	if f.failSeed {
		return errors.New("seed failed")
	}
	return SeedStore(ctx, f.store, s)
}
func (f *fakePlatform) SetConfig(ctx context.Context, inst Instance, cfg Config) error {
	f.calls = append(f.calls, "SetConfig")
	if _, err := cfg.Classify(nil); err != nil {
		return err
	}
	f.config = cfg
	return nil
}
func (f *fakePlatform) AttachDomain(ctx context.Context, inst Instance, domain string) (DomainStatus, error) {
	f.calls = append(f.calls, "AttachDomain")
	return DomainStatus{Domain: domain}, nil
}
func (f *fakePlatform) Activate(ctx context.Context, inst Instance, rel Release) error {
	f.calls = append(f.calls, "Activate")
	if f.failActivate {
		return errors.New("activation refused")
	}
	return nil
}
func (f *fakePlatform) Status(ctx context.Context, inst Instance) (Status, error) {
	return Status{State: StateUnknown}, nil
}
func (f *fakePlatform) Destroy(ctx context.Context, inst Instance) error {
	f.calls = append(f.calls, "Destroy")
	f.destroyed = true
	return nil
}

var _ Provisioner = (*fakePlatform)(nil)
