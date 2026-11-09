package site

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// LicenseFilename is the licence document's name inside its content directory.
const LicenseFilename = "license.json"

// LicenseKey is the licence document's key, relative to the site root.
//
// ⭐⭐ THIS IS THE SOURCE OF TRUTH: LicenseURLPath prefixes it with "/", and
// every read, write and delete of the document addresses it by this key.
// ⚠️ Before this, LicenseURLPath recovered the relative form by calling
// filepath.Rel on two absolute paths — a round trip that only worked because a
// path happened to be available, and that had a fallback arm for when Rel
// failed. A key is that relative form already.
//
// `dir` is user-configurable per content type, so a site that has moved its
// licence directory is a supported configuration, not a broken one. Falls back
// to the default layout when the bundle cannot be read.
func LicenseKey(site storage.Site) string {
	// ⚠️ bundle.LoadBundle takes a path: pkg/bundle is not on the serving path
	// and has not converted.
	b, err := bundle.LoadBundleAt(site, "content/pub.polis.core/bundle.json")
	if err == nil {
		if dir, err := b.ContentDir(license.TypeName); err == nil {
			return filepath.ToSlash(filepath.Join(dir, LicenseFilename))
		}
	}
	return "content/pub.polis.core/license/" + LicenseFilename
}

// LicenseURLPath returns the site-relative URL of the licence document — the
// value written into the `license` pointer in .well-known/polis.
func LicenseURLPath(site storage.Site) string {
	return "/" + LicenseKey(site)
}

// LicenseMountDir returns the site-relative directory the licence type's
// RENDERED pages are published under — the terms page and the per-version
// profile explanations — honouring the site's own bundle declaration.
//
// ⚠️ This is the MOUNT, not the content dir. license.json lives at
// LicenseKey (the signed source); this is where its projections go. They are
// configured separately and a site may move either one.
//
// One resolver, because two callers need the same answer for opposite reasons:
// the renderer writes those pages, and WithdrawLicense removes them. A second
// idea of where they live would leave one of the two acting on an empty
// directory.
func LicenseMountDir(site storage.Site) string {
	b, err := bundle.LoadBundleAt(site, "content/pub.polis.core/bundle.json")
	if err == nil {
		if dir, err := b.MountDir(license.TypeName); err == nil {
			return dir
		}
	}
	return "license"
}

// StateLicense writes a signed licence document and points .well-known/polis at
// it, in that order.
//
// This is the ONLY place both halves are written, and the ordering matters: the
// document exists before anything advertises it, so a crash between the two
// leaves an unreferenced file rather than a pointer into nothing.
//
// Returns (false, nil) for a choice of "none" — publishing no terms is a
// genuine option, not a failure. Forcing a declaration is its own imposition,
// and `unknown` is a defined state rather than a gap.
//
// ⚠️ Never call this on an author's behalf without their having chosen. A
// license.json is a signed statement of intent; creating one the author never
// made asserts they said something they did not.
func StateLicense(site storage.Site, choice, baseURL string, privateKey []byte) (bool, error) {
	profile, stated, err := license.ParseProfileName(choice)
	if err != nil {
		return false, err
	}
	if !stated {
		return false, nil
	}

	f, err := license.New(profile, baseURL)
	if err != nil {
		return false, err
	}
	key := LicenseKey(site)

	// `created` means CREATED. license.New builds a fresh file with created,
	// updated and asserted all set to now, which is right for a first
	// statement and wrong for a restatement — it would silently move the date
	// this site first stated terms, every time the author changed their mind or
	// re-ran the command.
	//
	// So carry the original forward when there is one. `updated` and `asserted`
	// still move, and they should: the author IS restating, now.
	//
	// ⚠️ A missing, unreadable or dateless existing file is not an error here.
	// It means there is nothing to carry forward, so the fresh value stands —
	// which is exactly what a first statement wants. Refusing to state terms
	// because an old file would not parse would be the worst possible trade.
	//
	// ⛔ Read through the STORE. A path read on a blob-backed site fails, and
	// because a failed read is deliberately "nothing to carry forward" here, it
	// would fail SILENTLY — every restatement would reset the date this site
	// first stated terms.
	if existing, err := license.LoadKey(site, key); err == nil && existing.Created != "" {
		f.Created = existing.Created
	}

	if err := license.SignAndWrite(f, site, key, privateKey); err != nil {
		return false, err
	}

	if err := SetLicensePointer(site, LicenseURLPath(site)); err != nil {
		return false, fmt.Errorf("write licence pointer: %w", err)
	}
	return true, nil
}

// WithdrawLicense removes the projections, the pointer and the document, in
// that order — the reverse of StateLicense, so a crash never leaves anything
// advertising something that is gone.
//
// ⛔ THE PROJECTIONS ARE PART OF THE WITHDRAWAL, NOT AN AFTERTHOUGHT. Removing
// only license.json leaves robots.txt, rsl.xml and the terms page asserting
// terms the site no longer states — published, machine-readable, and with
// nothing anywhere that would ever take them down. A projection must track its
// source in BOTH directions; this is the destroy half.
//
// ⛔ REMOVE, DO NOT BLANK. An empty robots.txt is a statement of its own. An
// absent one is the pre-terms state, which is exactly what withdrawal returns
// the site to: afterwards it is indistinguishable from a site that never
// stated any terms.
//
// ⚠️ ORDERING, and why it is this way round. StateLicense writes the document
// before the pointer that advertises it. Withdrawal is the mirror: take down
// the advertisements first, outermost first. A crash midway then leaves a site
// that still states terms and is missing some derived files — which the next
// render regenerates. The other order would leave published surfaces asserting
// withdrawn terms, and nothing repairs that.
//
// Withdrawal does NOT change any work already published: those carry the terms
// they were signed with, and no later act by the author reaches back to them.
func WithdrawLicense(site storage.Site) error {
	if err := RemoveLicenseSurfaces(site); err != nil {
		return err
	}
	if err := SetLicensePointer(site, ""); err != nil {
		return err
	}
	return site.Store().Delete(context.Background(), LicenseKey(site), storage.IgnoreMissing())
}

// RemoveLicenseSurfaces deletes every generated licence surface: robots.txt,
// rsl.xml, and the rendered pages under the licence type's mount.
//
// It removes only what the renderer writes — the terms page (index.html) and
// the permanent explanation of each profile version (reserved-1.html, …),
// recognised BY NAME — and then the directory itself if nothing else is in it.
// Any other file in the mount, .html or not, is the author's and is left
// intact, with the mount standing around it.
//
// ⚠️ It used to delete every top-level .html in the mount, which took an
// author's own notes.html with it on withdrawal.
//
// ⚠️ Safe to call on a site that has no surfaces: every removal tolerates
// absence. It is deliberately NOT called from a render — a render runs on every
// publish, and a site that never stated terms may have a robots.txt of its own
// that is none of our business. Removal belongs to the one act that means it:
// withdrawal.
func RemoveLicenseSurfaces(site storage.Site) error {
	for _, name := range []string{"robots.txt", "rsl.xml"} {
		if err := site.Store().Delete(context.Background(), name, storage.IgnoreMissing()); err != nil {
			return err
		}
	}

	mount := strings.Trim(LicenseMountDir(site), "/")
	// A mount that resolves to the site root is not a mount we may empty.
	if mount == "" || mount == "." {
		return nil
	}
	ctx := context.Background()

	// ⭐ List, not ListDir: a missing mount and an empty one mean the same
	// thing here — there is nothing of ours to remove — and that is exactly
	// List's contract. A read that fails for any other reason is still an
	// error.
	entries, err := site.Store().List(ctx, mount)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir || !isRenderedLicensePage(pathpkg.Base(e.Key)) {
			continue
		}
		if err := site.Store().Delete(ctx, mount+"/"+pathpkg.Base(e.Key), storage.IgnoreMissing()); err != nil {
			return err
		}
	}
	// A plain delete is the right call, not a recursive one: on a filesystem
	// it succeeds only on an empty directory, so anything the author left
	// there keeps the mount alive. A store with no directories holds no key at
	// the mount itself, so there is nothing to delete and absence is ignored.
	if err := site.Store().Delete(ctx, mount, storage.IgnoreMissing()); err != nil {
		if !isNotEmpty(err) {
			return err
		}
	}
	return nil
}

// isRenderedLicensePage reports whether name is a page the licence renderer
// writes (pkg/render/license_pages.go): index.html, or <slug>.html for a
// profile the licence package knows, where the slug is the profile tag minus
// "pub.polis.license." with "/" turned into "-". A name that merely looks like
// one — an unknown profile, a draft — is not ours to delete.
func isRenderedLicensePage(name string) bool {
	if name == "index.html" {
		return true
	}
	slug, ok := strings.CutSuffix(name, ".html")
	if !ok {
		return false
	}
	i := strings.LastIndex(slug, "-")
	if i <= 0 {
		return false
	}
	profile := "pub.polis.license." + slug[:i] + "/" + slug[i+1:]
	_, err := license.ProfileTerms(profile, "", "")
	return err == nil
}

// isNotEmpty reports whether err is the "directory not empty" refusal, which is
// an expected outcome here rather than a failure.
func isNotEmpty(err error) bool {
	var perr *os.PathError
	if errors.As(err, &perr) {
		return errors.Is(perr.Err, syscall.ENOTEMPTY) || errors.Is(perr.Err, syscall.EEXIST)
	}
	return false
}

// SiteTerms loads the site's current licence terms by FOLLOWING THE POINTER,
// which is the only supported way to find them.
//
// Returns (nil, nil) when the site has stated no terms. A pointer that leads
// nowhere is treated the same way: a consumer cannot tell the difference
// between "no terms" and "terms we failed to fetch", and inventing terms in
// either case would be worse than silence.
func SiteTerms(site storage.Site) (*license.Terms, error) {
	p := LicensePointer(site)
	if p == "" {
		// No pointer means the author has stated nothing, even if a stray file
		// exists. The pointer is the statement.
		return nil, nil
	}
	pointer := trimLeadingSlash(p)

	f, err := license.LoadKey(site, pointer)
	if err != nil {
		// ⚠️ A pointer that escapes the site leads nowhere too. When this
		// joined the pointer onto a path it read whatever file sat outside the
		// site; the store refuses the key, and that refusal is "nothing here",
		// the same as a pointer to a file that is gone.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, storage.ErrInvalidKey) {
			return nil, nil
		}
		return nil, err
	}
	if f.Terms == nil {
		return nil, nil
	}
	if err := f.Terms.Validate(); err != nil {
		return nil, fmt.Errorf("licence at %s is invalid: %w", pointer, err)
	}
	return f.Terms, nil
}

func trimLeadingSlash(s string) string {
	for len(s) > 0 && s[0] == '/' {
		s = s[1:]
	}
	return s
}
