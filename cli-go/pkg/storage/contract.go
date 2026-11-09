package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

// RunContract exercises every behaviour the Store interface promises, against
// any implementation.
//
// ⭐⭐⭐ IT LIVES IN THE PACKAGE, NOT IN A _test.go FILE, SO OTHER MODULES CAN
// RUN IT. A backend implemented in `webapp` or in a future module must be able
// to prove itself against the same suite; a test-only helper would force each
// one to write its own, and two suites that disagree are worse than one.
//
// ⚠️ It takes a factory rather than a Store: several checks need a store with
// nothing in it, and a suite that shares one store between checks cannot tell
// "absent" from "deleted by an earlier check".
//
// ⭐ The report is the return value rather than a *testing.T, for the same
// reason: this package must not import `testing` into a production build.
//
// ⚠️ WHAT THIS SUITE CANNOT TELL YOU. It tests storage behaviour against a
// backend, with no network: the Vercel Blob backend runs against a fake of its
// API. What only a real deployment shows is runtime, not logic, and nothing
// here covers it:
//   - the real Blob API's behaviour, where the fake might differ (listing lag
//     after a delete, rate limits, eventual consistency);
//   - a function's cold start and duration caps;
//   - cron firing, and its granularity on each plan;
//   - deploying, confirming the deployment came up, and rolling back;
//   - serving on the instance's domain, including its rewrites.
//
// Those are checked from outside, against a running instance: `polis validate
// <url>`, and the fleet canary for signatures.
func RunContract(ctx context.Context, fresh func() (Store, func())) ContractReport {
	r := ContractReport{}
	for _, c := range contractChecks {
		s, cleanup := fresh()
		err := c.fn(ctx, s)
		if cleanup != nil {
			cleanup()
		}
		switch {
		case err == nil:
			r.Passed = append(r.Passed, c.name)
		case errors.Is(err, errContractSkip):
			r.Skipped = append(r.Skipped, fmt.Sprintf("%s: %v", c.name, err))
		default:
			r.Failed = append(r.Failed, fmt.Sprintf("%s: %v", c.name, err))
		}
	}
	return r
}

// ContractReport is what a backend scored.
type ContractReport struct {
	Passed  []string
	Failed  []string
	Skipped []string
}

// OK reports whether every applicable check passed.
func (r ContractReport) OK() bool { return len(r.Failed) == 0 }

func (r ContractReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d passed, %d failed, %d skipped", len(r.Passed), len(r.Failed), len(r.Skipped))
	for _, f := range r.Failed {
		fmt.Fprintf(&b, "\n  FAIL %s", f)
	}
	for _, s := range r.Skipped {
		fmt.Fprintf(&b, "\n  skip %s", s)
	}
	return b.String()
}

// errContractSkip marks a check that does not apply to a backend, which is
// different from one that failed.
//
// ⛔ A SKIP MUST NAME THE CAPABILITY IT TURNED ON, so a reader can tell "this
// backend cannot do that, by declaration" from "this suite forgot to check".
var errContractSkip = errors.New("not applicable")

func skip(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errContractSkip, fmt.Sprintf(format, a...))
}

type contractCheck struct {
	name string
	fn   func(context.Context, Store) error
}

var contractChecks = []contractCheck{
	{"get-absent-is-ErrNotFound", cGetAbsent},
	{"put-then-get-round-trips", cPutGet},
	{"put-replaces", cPutReplaces},
	{"put-if-absent-refuses-an-existing-key", cPutIfAbsent},
	{"put-if-absent-is-exclusive-under-concurrency", cPutIfAbsentRace},
	{"put-creates-parents-implicitly", cImplicitParents},
	{"exists-answers-both-ways", cExists},
	{"stat-absent-is-ErrNotFound", cStatAbsent},
	{"stat-reports-size", cStatSize},
	{"list-absent-prefix-is-empty-not-error", cListAbsent},
	{"list-returns-direct-children-only", cListShallow},
	{"list-recursive-returns-the-subtree", cListRecursive},
	{"list-empty-prefix-lists-everything", cListRoot},
	{"delete-absent-is-ErrNotFound", cDeleteAbsent},
	{"delete-absent-with-IgnoreMissing-is-nil", cDeleteIgnoreMissing},
	{"delete-recursive-removes-the-subtree", cDeleteRecursive},
	{"keys-are-slash-separated-and-not-rooted", cKeyShape},
	{"malformed-keys-are-refused-identically", cMalformedKeys},
	{"the-root-cannot-be-deleted", cRootDelete},
	{"capabilities-are-self-consistent", cCapabilities},
	{"modes-are-honoured-when-declared", cModes},
	{"realpath-matches-its-capability", cRealPath},
}

// ⛔ A conditional create that a backend silently treats as a plain Put is the
// two-identities bug with every test green — so both halves are checked: it
// refuses, and it leaves what was there untouched.
func cPutIfAbsent(ctx context.Context, s Store) error {
	if err := s.Put(ctx, "once/key", []byte("first"), IfAbsent()); err != nil {
		return fmt.Errorf("IfAbsent Put of an absent key failed: %v", err)
	}
	err := s.Put(ctx, "once/key", []byte("second"), IfAbsent())
	if !errors.Is(err, ErrExists) {
		return fmt.Errorf("IfAbsent Put of an existing key gave %v, want ErrExists", err)
	}
	got, err := s.Get(ctx, "once/key")
	if err != nil {
		return err
	}
	if string(got) != "first" {
		return fmt.Errorf("a refused IfAbsent Put changed the object to %q", got)
	}
	return nil
}

// ⭐ The case that matters: concurrent creators, exactly one winner, and the
// object holds the winner's bytes.
func cPutIfAbsentRace(ctx context.Context, s Store) error {
	const n = 16
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.Put(ctx, "race/key", []byte(fmt.Sprintf("writer-%02d", i)), IfAbsent())
		}(i)
	}
	wg.Wait()
	close(errs)
	won := 0
	for err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrExists):
		default:
			return fmt.Errorf("a racing IfAbsent Put failed with %v, want nil or ErrExists", err)
		}
	}
	if won != 1 {
		return fmt.Errorf("%d of %d concurrent IfAbsent Puts won; exactly one may", won, n)
	}
	got, err := s.Get(ctx, "race/key")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(got), "writer-") || len(got) != len("writer-00") {
		return fmt.Errorf("the winner's object is %q — torn or mixed", got)
	}
	return nil
}

// ⛔ Every backend refuses the same malformed keys. A backend with its own,
// looser rules is a traversal bug waiting for a caller that passes input: the
// object store once turned an absolute path into a key and accepted NUL and
// whitespace, and POSIX once trimmed whitespace AFTER a deny check had run.
func cMalformedKeys(ctx context.Context, s Store) error {
	for _, k := range []string{
		" .polis/x", ".polis/x ", "\t.polis/x", "\u00a0.polis/x",
		"/abs/a.txt", `a\b.txt`, `..\..\outside.txt`, "a\x00b", "../x", "a/../../x",
	} {
		if err := s.Put(ctx, k, []byte("x")); err == nil {
			return fmt.Errorf("Put(%q) was accepted; every backend must refuse it", k)
		}
		if _, err := s.Get(ctx, k); err == nil || errors.Is(err, ErrNotFound) {
			return fmt.Errorf("Get(%q) gave %v; it must be refused as invalid, not looked up", k, err)
		}
	}
	return nil
}

// ⛔ A recursive delete of the root would wipe .polis/keys. No backend allows it.
func cRootDelete(ctx context.Context, s Store) error {
	if err := s.Put(ctx, ".polis/keys/id_ed25519", []byte("k")); err != nil {
		return err
	}
	for _, root := range []string{"", "/", "."} {
		if err := s.Delete(ctx, root, DeleteRecursive()); err == nil {
			return fmt.Errorf("a recursive Delete of %q was accepted", root)
		}
	}
	if ok, _ := s.Exists(ctx, ".polis/keys/id_ed25519"); !ok {
		return fmt.Errorf("the key is gone after a refused root delete")
	}
	return nil
}

func cGetAbsent(ctx context.Context, s Store) error {
	_, err := s.Get(ctx, "nothing/here.txt")
	if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("Get of an absent key gave %v, want ErrNotFound", err)
	}
	// ⛔ The wrap matters as much as the sentinel: callers across the tree test
	// with errors.Is(err, fs.ErrNotExist), and ErrNotFound wraps it.
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("ErrNotFound does not wrap fs.ErrNotExist; every caller that "+
			"checks for absence with errors.Is(err, fs.ErrNotExist) would miss it (got %v)", err)
	}
	return nil
}

func cPutGet(ctx context.Context, s Store) error {
	want := []byte("hello\nworld\n")
	if err := s.Put(ctx, "a/b/c.txt", want); err != nil {
		return err
	}
	got, err := s.Get(ctx, "a/b/c.txt")
	if err != nil {
		return err
	}
	if string(got) != string(want) {
		return fmt.Errorf("Get returned %q, want %q", got, want)
	}
	return nil
}

func cPutReplaces(ctx context.Context, s Store) error {
	if err := s.Put(ctx, "k", []byte("first")); err != nil {
		return err
	}
	if err := s.Put(ctx, "k", []byte("second")); err != nil {
		return err
	}
	got, err := s.Get(ctx, "k")
	if err != nil {
		return err
	}
	if string(got) != "second" {
		return fmt.Errorf("Put did not replace: got %q", got)
	}
	return nil
}

func cImplicitParents(ctx context.Context, s Store) error {
	// ⭐ "Parent prefixes are implicit: a caller never creates one." A POSIX
	// store must mkdir; an object store has nothing to do. Either way the
	// caller writes one deep key and it works.
	if err := s.Put(ctx, "deep/er/est/file.txt", []byte("x")); err != nil {
		return fmt.Errorf("Put into an uncreated prefix failed: %w", err)
	}
	if _, err := s.Get(ctx, "deep/er/est/file.txt"); err != nil {
		return err
	}
	return nil
}

func cExists(ctx context.Context, s Store) error {
	if ok, err := s.Exists(ctx, "absent"); err != nil || ok {
		return fmt.Errorf("Exists(absent) = %v, %v; want false, nil", ok, err)
	}
	if err := s.Put(ctx, "present", []byte("x")); err != nil {
		return err
	}
	if ok, err := s.Exists(ctx, "present"); err != nil || !ok {
		return fmt.Errorf("Exists(present) = %v, %v; want true, nil", ok, err)
	}
	return nil
}

func cStatAbsent(ctx context.Context, s Store) error {
	_, err := s.Stat(ctx, "absent")
	if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("Stat of an absent key gave %v, want ErrNotFound", err)
	}
	return nil
}

func cStatSize(ctx context.Context, s Store) error {
	data := []byte("0123456789")
	if err := s.Put(ctx, "sized", data); err != nil {
		return err
	}
	info, err := s.Stat(ctx, "sized")
	if err != nil {
		return err
	}
	if info.Size != int64(len(data)) {
		return fmt.Errorf("Stat reported size %d, want %d", info.Size, len(data))
	}
	return nil
}

func cListAbsent(ctx context.Context, s Store) error {
	// ⛔ "A missing prefix is an empty result, not ErrNotFound." Callers read
	// a List error as "I could not look", so absence must not produce one.
	got, err := s.List(ctx, "no/such/prefix")
	if err != nil {
		return fmt.Errorf("List of an absent prefix errored: %w", err)
	}
	if len(got) != 0 {
		return fmt.Errorf("List of an absent prefix returned %d entries", len(got))
	}
	return nil
}

func cListShallow(ctx context.Context, s Store) error {
	for _, k := range []string{"d/one.txt", "d/two.txt", "d/sub/three.txt", "other.txt"} {
		if err := s.Put(ctx, k, []byte("x")); err != nil {
			return err
		}
	}
	got, err := s.List(ctx, "d")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range got {
		names = append(names, lastSeg(e.Key)+boolSuffix(e.IsDir))
	}
	sort.Strings(names)
	want := "one.txt,sub/,two.txt"
	if strings.Join(names, ",") != want {
		return fmt.Errorf("List(d) = %v, want %s — direct children only, with the "+
			"subdirectory reported as a directory entry", names, want)
	}
	return nil
}

func cListRecursive(ctx context.Context, s Store) error {
	for _, k := range []string{"r/a.txt", "r/s/b.txt", "r/s/t/c.txt", "elsewhere.txt"} {
		if err := s.Put(ctx, k, []byte("x")); err != nil {
			return err
		}
	}
	got, err := s.List(ctx, "r", Recursive())
	if err != nil {
		return err
	}
	var files []string
	for _, e := range got {
		if !e.IsDir {
			files = append(files, e.Key)
		}
	}
	sort.Strings(files)
	want := "r/a.txt,r/s/b.txt,r/s/t/c.txt"
	if strings.Join(files, ",") != want {
		return fmt.Errorf("recursive List(r) files = %v, want %s", files, want)
	}
	return nil
}

func cListRoot(ctx context.Context, s Store) error {
	for _, k := range []string{"top.txt", "nested/in.txt"} {
		if err := s.Put(ctx, k, []byte("x")); err != nil {
			return err
		}
	}
	got, err := s.List(ctx, "", Recursive())
	if err != nil {
		return err
	}
	var files []string
	for _, e := range got {
		if !e.IsDir {
			files = append(files, e.Key)
		}
	}
	sort.Strings(files)
	if strings.Join(files, ",") != "nested/in.txt,top.txt" {
		return fmt.Errorf("List(\"\", Recursive) = %v, want both keys", files)
	}
	return nil
}

func cDeleteAbsent(ctx context.Context, s Store) error {
	err := s.Delete(ctx, "absent")
	if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("Delete of an absent key gave %v, want ErrNotFound", err)
	}
	return nil
}

func cDeleteIgnoreMissing(ctx context.Context, s Store) error {
	if err := s.Delete(ctx, "absent", IgnoreMissing()); err != nil {
		return fmt.Errorf("Delete(IgnoreMissing) of an absent key errored: %w", err)
	}
	return nil
}

func cDeleteRecursive(ctx context.Context, s Store) error {
	for _, k := range []string{"x/a.txt", "x/y/b.txt", "keep.txt"} {
		if err := s.Put(ctx, k, []byte("x")); err != nil {
			return err
		}
	}
	if err := s.Delete(ctx, "x", DeleteRecursive()); err != nil {
		return err
	}
	if ok, _ := s.Exists(ctx, "x/y/b.txt"); ok {
		return errors.New("recursive Delete left a nested key behind")
	}
	if ok, err := s.Exists(ctx, "keep.txt"); err != nil || !ok {
		return errors.New("recursive Delete removed a key outside the prefix")
	}
	return nil
}

func cKeyShape(ctx context.Context, s Store) error {
	// ⚠️ Keys are slash-separated and relative. A leading slash, a backslash
	// or a traversal is a caller bug, and a store must not resolve one into a
	// neighbour's data.
	if err := s.Put(ctx, "shape/ok.txt", []byte("x")); err != nil {
		return err
	}
	for _, bad := range []string{"../escape.txt", "shape/../../escape.txt"} {
		if err := s.Put(ctx, bad, []byte("x")); err == nil {
			return fmt.Errorf("Put(%q) succeeded; a traversing key must be refused", bad)
		}
	}
	return nil
}

func cCapabilities(ctx context.Context, s Store) error {
	c := s.Capabilities()
	// ⭐ RealPath the capability and RealPath the method must agree; that is
	// checked separately. Here: a store claiming modes must also claim to be
	// able to report them through Stat.
	if c.Modes && !c.ModTimes {
		// Not a contradiction in principle, but no backend we have does this,
		// and a backend that enforces modes without timestamps is worth a
		// second look rather than a silent pass.
		return fmt.Errorf("declares Modes without ModTimes; that combination is untested — "+
			"state why in the backend's own comment if it is deliberate (caps=%+v)", c)
	}
	return nil
}

func cModes(ctx context.Context, s Store) error {
	if !s.Capabilities().Modes {
		return skip("Capabilities().Modes is false")
	}
	if err := s.Put(ctx, "moded.txt", []byte("x"), WithMode(0o600)); err != nil {
		return err
	}
	info, err := s.Stat(ctx, "moded.txt")
	if err != nil {
		return err
	}
	if info.Mode.Perm() != 0o600 {
		return fmt.Errorf("Put(WithMode(0600)) then Stat gave mode %04o; a store that "+
			"declares Modes must honour them — the .polis privacy rule depends on it",
			info.Mode.Perm())
	}
	return nil
}

func cRealPath(ctx context.Context, s Store) error {
	_, err := s.RealPath("k")
	if s.Capabilities().RealPath {
		if err != nil {
			return fmt.Errorf("declares RealPath but returned %v", err)
		}
		return nil
	}
	if !errors.Is(err, ErrUnsupported) {
		return fmt.Errorf("does not declare RealPath, so RealPath must return "+
			"ErrUnsupported rather than %v — a caller that ignores the capability "+
			"and uses the result would otherwise get a path that does not exist", err)
	}
	return nil
}

func lastSeg(k string) string {
	if i := strings.LastIndex(strings.TrimSuffix(k, "/"), "/"); i >= 0 {
		return strings.TrimSuffix(k, "/")[i+1:]
	}
	return strings.TrimSuffix(k, "/")
}

func boolSuffix(isDir bool) string {
	if isDir {
		return "/"
	}
	return ""
}
