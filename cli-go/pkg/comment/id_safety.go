package comment

import (
	"fmt"
	"strings"
)

// validCommentID rejects an ID that could address anything but a file directly
// inside the directory the caller chose.
//
// ⛔⛔ THIS IS A CROSS-TENANT WRITE DEFENCE, not tidiness. Every path here is
// built as filepath.Join(<dir>, id+".md"), so an ID containing "../" walks out of
// the comments tree. Reproduced before this existed: an ID of six "../" segments
// plus "bob/pwned" wrote tenants/bob/pwned.md — INTO ANOTHER TENANT'S SITE on the
// shared hosted volume.
//
// ⚠️ The detach made it materially worse rather than introducing it. Before,
// os.WriteFile could only land in directories that already existed; the writes
// now go through storage.WriteAtPath, which CREATES MISSING PARENTS, so a
// traversing ID can build the tree it needs on the way out.
//
// ⭐ It lives in this package on purpose. One HTTP handler
// (webapp/internal/server/handlers.go) passed a request-supplied ID straight in
// while its four siblings sanitised, and a package that depends on its caller
// remembering is one refactor from the same hole.
func validCommentID(id string) error {
	switch {
	case id == "":
		return fmt.Errorf("comment id is empty")
	case strings.ContainsAny(id, `/\`):
		return fmt.Errorf("comment id %q contains a path separator", id)
	case strings.Contains(id, ".."):
		return fmt.Errorf("comment id %q contains %q", id, "..")
	case strings.HasPrefix(id, "."):
		return fmt.Errorf("comment id %q starts with a dot", id)
	case strings.ContainsRune(id, 0):
		return fmt.Errorf("comment id contains a NUL byte")
	}
	return nil
}

// lookupCommentID is the rule for an id that addresses a comment ALREADY ON DISK:
// LoadDraft, DeleteDraft, GetComment, MoveComment, PublishComment and the rest.
//
// ⛔ Every one of those builds filepath.Join(<dir>, id+".md") exactly as the
// writers do, so a traversing id reads, moves or deletes a file somewhere else.
// The first fix guarded the two writes and left these open: LoadDraft read
// another tenant's draft, DeleteDraft removed it, and the sign handler published
// what LoadDraft had read as the caller's own comment. Ids reach here from an
// HTTP request body and from discovery-service events, so neither caller can be
// trusted to have cleaned them.
//
// ⭐ It is NARROWER than validCommentID on purpose. That rule decides what may be
// CREATED and also refuses ".." anywhere and a leading dot. This one must never
// strand a comment that exists: without a separator an id names one file inside
// the chosen directory whatever else it contains, so "x..y" and ".hidden" stay
// reachable.
func lookupCommentID(id string) error {
	switch {
	case id == "":
		return fmt.Errorf("comment id is empty")
	case strings.ContainsAny(id, `/\`):
		return fmt.Errorf("comment id %q contains a path separator", id)
	case strings.ContainsRune(id, 0):
		return fmt.Errorf("comment id contains a NUL byte")
	}
	return nil
}

// knownStatus refuses a status that is not one of the four this package defines.
//
// ⛔ A status is a PATH SEGMENT — .polis/…/comments/<status>/<id>.md — so it
// traverses as readily as an id does.
func knownStatus(status string) error {
	switch status {
	case StatusDrafts, StatusPending, StatusBlessed, StatusDenied:
		return nil
	}
	return fmt.Errorf("unknown comment status %q", status)
}
