package tailor

// ⭐ THE ONE DECLARATION OF TAILOR'S CHECKS. Run order, `tailor --list`, the
// --only/--skip dependency refusal and the public upgrade guide's drift test
// all read this table, so they cannot disagree.
//
// ⛔ Ids and their order are a published shape: Patrol baselines, operator
// tooling and the golden testdata read them. Add a check by adding a row; do
// not rename or reorder one.

// Kind says how freely a check may be taken on its own.
type Kind string

const (
	// KindChain moves something later checks assume has moved.
	KindChain Kind = "chain"
	// KindProvision creates something missing and changes nothing you had.
	KindProvision Kind = "provision"
	// KindOverwrite replaces something you may have changed yourself.
	KindOverwrite Kind = "overwrite"
	// KindCleanup removes something obsolete.
	KindCleanup Kind = "cleanup"
	// KindReport never changes anything.
	KindReport Kind = "report"
)

// CheckDef declares one check.
type CheckDef struct {
	ID        string   `json:"id"`
	Phase     string   `json:"phase"`
	Kind      Kind     `json:"kind"`
	Summary   string   `json:"summary"`
	DependsOn []string `json:"depends_on,omitempty"` // must already pass, or be selected too
	fn        checkFunc
}

// beforeDerived are the migrations the index rebuild and render read the
// results of ("all must run before Phase 5" in the code they came from).
var beforeDerived = []string{
	"layout-posts", "layout-comments", "layout-following", "layout-blessed", "layout-private-drafts",
	"bundle-reference-payload", "legacy-content-path", "registry-migration", "legacy-theme-locations",
	"bundle-active-fields", "notification-rules-migration", "active-shape-upgrade", "v3-legacy-archives",
}

func checkDefs() []CheckDef {
	return []CheckDef{
		// Phase 1: Well-known identity
		{"wellknown-version", "1", KindProvision, "version field in generator format (polis-cli-go/X.Y.Z)", nil, checkWellKnownVersion},
		{"author-field-migration", "1", KindChain, "rename .well-known/polis author to author_name", nil, checkAuthorFieldMigration},
		{"wellknown-legacy-config", "1", KindCleanup, "remove config, domain, base_url, subdomain from .well-known/polis", nil, checkWellKnownLegacyConfig},
		{"wellknown-bundles", "1", KindChain, "declare the core bundle in .well-known/polis", nil, checkWellKnownBundles},
		{"avatar-config", "1", KindProvision, "add a default avatar and favicon.svg", nil, checkAvatarConfig},
		// Phase 2: Bundle definition
		{"bundle-json", "2", KindChain, "create bundle.json, or add the content types it is missing", nil, checkBundleJSON},
		// Phase 3: Layout migration
		{"layout-posts", "3", KindChain, "move post sources from posts/ to content/pub.polis.core/post/", nil, checkLayoutPosts},
		{"layout-comments", "3", KindChain, "move comment sources from comments/ to content/pub.polis.core/comment/", nil, checkLayoutComments},
		{"layout-following", "3", KindChain, "move metadata/following.json into the core bundle", nil, checkLayoutFollowing},
		{"layout-blessed", "3", KindChain, "move metadata/blessed-comments.json into the core bundle", nil, checkLayoutBlessed},
		// Phase 4: Cross-platform fixes
		{"path-separators", "4", KindOverwrite, "replace Windows backslash paths in index and metadata files", nil, checkPathSeparators},
		// Phase 4.5: bundle/registry/theme refactor migrations
		{"bundle-reference-payload", "4.5", KindOverwrite, "install or refresh the shipped shapes and themes", nil, checkBundleReferencePayload},
		{"legacy-content-path", "4.5", KindChain, "move private state from .polis/content/ to .polis/bundles/", nil, checkLegacyContentPath},
		// After legacy-content-path, which refuses to merge into a subtree that
		// exists: moving drafts first would create one and strand the rest.
		{"layout-private-drafts", "4.5", KindChain, "move pre-0.58 drafts and pending/denied comments into .polis/bundles/", []string{"legacy-content-path"}, checkLayoutPrivateDrafts},
		{"registry-migration", "4.5", KindChain, "move active_theme from .well-known/polis to the registry", []string{"bundle-reference-payload"}, checkRegistryMigration},
		{"legacy-theme-locations", "4.5", KindOverwrite, "remove old copies of shipped themes; keep your own", []string{"bundle-reference-payload"}, checkLegacyThemeLocations},
		{"bundle-active-fields", "4.5", KindCleanup, "remove the unused active flag on bundles", nil, checkBundleActiveFields},
		// Phase 4.7: later migrations
		{"notification-rules-migration", "4.7", KindChain, "move notification rules from bundle.json to the registry", nil, checkNotificationRulesMigration},
		{"active-shape-upgrade", "4.7", KindReport, "report a site on the blog (v3) or stream (v4) shape, no longer supported, and how to switch to v5; never switches", nil, checkActiveShapeUpgrade},
		{"v3-legacy-archives", "4.7", KindCleanup, "remove stale v3 archive pages on a v4 site", []string{"active-shape-upgrade"}, checkV3LegacyArchives},
		// Phase 5 and 5.5: derived data
		{"index-rebuild", "5", KindProvision, "rebuild index.jsonl from the signed content", beforeDerived, checkIndexRebuild},
		{"render-site", "5.5", KindProvision, "re-render the site's HTML", append(append([]string{}, beforeDerived...), "index-rebuild"), checkRenderSite},
		// Phase 6: Provisioning
		{"policies-public", "6", KindProvision, "create the default public policy file", nil, checkPoliciesPublic},
		{"policies-private", "6", KindProvision, "create the default private policy file", nil, checkPoliciesPrivate},
		{"policy-content-converge", "6", KindOverwrite, "replace a policy file that still holds an old default; never your own rules", nil, checkPolicyContentConverge},
		{"tag-directory", "6", KindProvision, "create the tag directory", nil, checkTagDirectory},
		{"storage-salt", "6", KindProvision, "create the DM storage salt", nil, checkStorageSalt},
		{"dm-directories", "6", KindProvision, "create the private DM conversations directory", []string{"legacy-content-path"}, checkDMDirectories},
		{"dm-keyring", "6", KindProvision, "create the DM keyring and publish its key (signs with your key)", []string{"legacy-content-path"}, checkDMKeyring},
		{"theme-consolidation", "6", KindCleanup, "remove theme files that duplicate _base", nil, checkThemeConsolidation},
		// Phase 7: Config migration
		{"webapp-config", "7", KindChain, "move .polis/webapp-config.json to .polis/webapp/config.json", nil, checkWebappConfig},
		{"webapp-view-mode", "7", KindCleanup, "remove the deprecated view_mode setting", []string{"webapp-config"}, checkWebappViewMode},
		// Phase 6.5: Content-aware integrity
		{"reference-payload-integrity", "6.5", KindOverwrite, "reinstall shipped files you edited (your copies go to the backup)", nil, checkReferencePayloadIntegrity},
		{"registry-integrity", "6.5", KindOverwrite, "repair a malformed active theme or shape; keep your own theme", nil, checkRegistryIntegrity},
		{"key-consistency", "6.5", KindReport, "report a key file that disagrees with the published key", nil, checkKeyConsistency},
		{"bundle-path-integrity", "6.5", KindChain, "point .well-known/polis at the core bundle; report an empty author_name", []string{"author-field-migration"}, checkBundlePathIntegrity},
		{"unpublished-emits-migration", "6.5", KindProvision, "declare the unpublished events on posts and comments", nil, checkUnpublishedEmitsMigration},
		{"theme-shapes-migration", "6.5", KindProvision, "add the default compatible shapes to each theme", nil, checkThemeShapesMigration},
		{"bundle-declarations", "6.5", KindProvision, "add missing declarations; report drift in ones you have", []string{"unpublished-emits-migration", "theme-shapes-migration"}, checkBundleDeclarations},
		{"index-entries", "6.5", KindReport, "report malformed index.jsonl entries", nil, checkIndexEntries},
		{"blessed-following-structure", "6.5", KindReport, "report structural problems in blessed.json and following.json", nil, checkBlessedFollowingStructure},
		// Phase 6.7: Tailor-only self-heal
		{"active-theme-unset", "6.7", KindProvision, "pick a theme when none is active", nil, checkActiveThemeUnset},
		{"registry-schema-version", "6.7", KindReport, "report a registry schema older than this build", nil, checkRegistrySchemaVersion},
		{"registry-fqn-sanity", "6.7", KindReport, "report malformed theme or shape names", nil, checkRegistryFQNSanity},
		// Phase 8: Cleanup
		{"manifest-obsolete", "8", KindCleanup, "remove metadata/manifest.json", nil, checkManifestObsolete},
		{"empty-metadata-dir", "8", KindCleanup, "remove an empty metadata/ directory", nil, checkEmptyMetadataDir},
		{"foreign-content-in-public-path", "8", KindOverwrite, "remove other authors' content from your public paths", nil, checkForeignContentInPublicPath},
		{"blessed-cache-gc", "8", KindCleanup, "remove cached blessed comments that lost their provenance", nil, checkBlessedCacheGC},
		{"stale-scoped-feed", "8", KindCleanup, "remove the retired followers/me feed caches", nil, checkStaleScopedFeed},
		{"stale-feed-viewed-at", "8", KindCleanup, "remove the retired time-based feed cursor", nil, checkStaleFeedViewedAt},
		{"legacy-feed-scaffolding", "8", KindCleanup, "remove the retired pub.polis.feed content type", nil, checkLegacyFeedScaffolding},
		{"orphaned-theme-dirs", "8", KindCleanup, "remove leftovers of shipped themes; keep your own", nil, checkOrphanedThemeDirs},
		{"studio13-rename-migration", "8", KindOverwrite, "switch the retired studio13 theme to studio13-nk", nil, checkStudio13RenameMigration},
		// Phase 9: CLI binary (network, runs last)
		{"cli-update", "9", KindReport, "report a newer polis release and where to get it (never downloads)", nil, checkCLIUpdate},
	}
}

// Checks returns the declared checks in run order (for `tailor --list`).
func Checks() []CheckDef { return checkDefs() }

// allChecks returns the ordered list of checks to run.
func allChecks() []checkFunc {
	defs := checkDefs()
	out := make([]checkFunc, len(defs))
	for i, d := range defs {
		out[i] = d.fn
	}
	return out
}
