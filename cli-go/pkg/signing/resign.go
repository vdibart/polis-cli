package signing

// Re-signing at key rotation — the vocabulary the follow file and the blessing
// list share.
//
// Neither file carries a signing time, so neither can be resolved through the
// key history: each verifies against the CURRENT key only. A rotation that left
// them alone would turn both `invalid` until the author next followed or
// blessed. The author's own rotation re-signs them instead, the way it already
// re-signs public_key_messages.
//
// ⛔ ONLY A FILE THAT VERIFIES UNDER THE OLD KEY IS RE-SIGNED. The messages key
// is rebuilt from the private keyring, so the key vouches for its own source.
// These two files are their own source: a blind re-sign would vouch for whatever
// is on disk, turning a list that was already failing — tampered, or broken by a
// bug — into a valid one under the new key, and erasing the evidence.
//
// ⛔ AND ONLY IN THE OWNER'S ROTATION PATHS. This is the author acting on her
// own instance with her own key. No operator actor re-signs either file.

// ResignOutcome says what a rotation did with one signed file.
type ResignOutcome string

const (
	// ResignDone — the file verified under the old key and is now signed with
	// the new one.
	ResignDone ResignOutcome = "resigned"
	// ResignAbsent — there is no file. Nothing is created.
	ResignAbsent ResignOutcome = "absent"
	// ResignUnsigned — the file carries no signature and keeps none. Signing it
	// here would be a backfill the author never asked for.
	ResignUnsigned ResignOutcome = "unsigned"
	// ResignNotValid — the signature did not verify under the old key, so the
	// file is left exactly as it was.
	ResignNotValid ResignOutcome = "not-valid-under-old-key"
	// ResignRefused — the file carries members this build does not model, and
	// GuardRewrite refused to rewrite it. The error is a *RewriteRefusedError.
	ResignRefused ResignOutcome = "refused"
)
