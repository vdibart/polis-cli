package fleettest

import (
	"os"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// published is the signing time every fixture post and comment claims.
const published = "2026-01-15T10:00:00Z"

const (
	wellKnown  = ".well-known/polis"
	bundleJSON = "content/pub.polis.core/bundle.json"
	registry   = ".polis/bundles/registry.json"
	indexJSONL = "content/pub.polis.core/index.jsonl"
	blessed    = "content/pub.polis.core/comment/blessed.json"
	following  = "content/pub.polis.core/follow/following.json"
	pubPolicy  = "policies/rules.jsonl"
	privPolicy = ".polis/policies/rules.jsonl"
	convDir    = ".polis/bundles/pub.polis.core/dm/conversations"
	themesDir  = ".polis/bundles/pub.polis.core/themes"
	dsState    = ".polis/ds/" + DSDomain + "/pub.polis.core/state"
)

// C1Tenant is the one fixture whose key file and published key disagree —
// the only tenant whose golden the published-key change was allowed to move.
const C1Tenant = "c1-key-file-disagrees"

// Tenants is the fleet, in the order the sweep visits it (os.ReadDir sorts).
// One healthy site; every other tenant breaks exactly one thing.
func Tenants() []Tenant {
	return []Tenant{
		{Name: "healthy"},
		{Name: "discover", Break: func(t testing.TB, s *Site) {
			// The quip supply the hosted envelope reports on: 7 left of 25 → low (≤10).
			s.Write(t, ".polis/quips.txt", strings.Repeat("a quip\n", 25), 0600)
			s.Write(t, ".polis/quips-used.txt", strings.Repeat("a quip\n", 18), 0600)
		}},

		// ── Passed() — each fails one check the hosted envelope puts on patrol.fail
		{Name: "wk-missing", Break: func(t testing.TB, s *Site) { s.Remove(t, wellKnown) }},
		{Name: "wk-no-public-key", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) { m["public_key"] = "" })
		}},
		{Name: "pubkey-file-missing", Break: func(t testing.TB, s *Site) { s.Remove(t, ".polis/keys/id_ed25519.pub") }},
		{Name: "privkey-invalid", Break: func(t testing.TB, s *Site) {
			s.Write(t, ".polis/keys/id_ed25519", "not a key\n", 0600)
		}},
		{Name: "key-perms-unsafe", Break: func(t testing.TB, s *Site) { s.Chmod(t, ".polis/keys/id_ed25519", 0644) }},
		{Name: "key-leak", Break: func(t testing.TB, s *Site) {
			s.Write(t, "site/snippets/leak.html", "<pre>-----BEGIN OPENSSH PRIVATE KEY-----</pre>\n", 0644)
		}},
		{Name: "structure-no-keys-dir", Break: func(t testing.TB, s *Site) { s.Remove(t, ".polis/keys") }},
		{Name: "dir-exposed", Break: func(t testing.TB, s *Site) { s.Chmod(t, ".polis", 0755) }},
		{Name: "bundle-missing", Break: func(t testing.TB, s *Site) { s.Remove(t, bundleJSON) }},
		{Name: "bundle-invalid", Break: func(t testing.TB, s *Site) { s.Write(t, bundleJSON, "{not json", 0644) }},
		{Name: "bundle-no-name", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, bundleJSON, func(m map[string]any) { delete(m, "name") })
		}},
		{Name: "index-invalid-json", Break: func(t testing.TB, s *Site) {
			s.Write(t, indexJSONL, "{not json\n", 0644)
		}},
		{Name: "index-missing-field", Break: func(t testing.TB, s *Site) {
			s.Write(t, indexJSONL, `{"type":"pub.polis.post","path":"x.md","published":"2026-01-15T10:00:00Z"}`+"\n", 0644)
		}},
		{Name: "index-bad-version", Break: func(t testing.TB, s *Site) {
			s.Write(t, indexJSONL, `{"type":"pub.polis.post","path":"x.md","published":"2026-01-15T10:00:00Z","current_version":"md5:abc"}`+"\n", 0644)
		}},
		{Name: "index-bad-published", Break: func(t testing.TB, s *Site) {
			s.Write(t, indexJSONL, `{"type":"pub.polis.post","path":"x.md","published":"yesterday","current_version":"sha256:abc"}`+"\n", 0644)
		}},
		{Name: "index-many-problems", Break: func(t testing.TB, s *Site) {
			// Patrol reports the first; Tailor counts them all.
			s.Write(t, indexJSONL, "\n"+
				`{"type":"pub.polis.post","path":"","published":"yesterday","current_version":"md5:abc"}`+"\n"+
				"{not json\n", 0644)
		}},
		{Name: "blessed-invalid-json", Break: blessedWith("{not json")},
		{Name: "blessed-no-comments", Break: blessedWith(`{"version":"x"}`)},
		{Name: "blessed-comments-not-array", Break: blessedWith(`{"comments":{}}`)},
		{Name: "blessed-group-not-object", Break: blessedWith(`{"comments":["x"]}`)},
		{Name: "blessed-group-no-post", Break: blessedWith(`{"comments":[{"blessed":[]}]}`)},
		{Name: "blessed-group-no-blessed", Break: blessedWith(`{"comments":[{"post":"p.md"}]}`)},
		{Name: "blessed-blessed-not-array", Break: blessedWith(`{"comments":[{"post":"p.md","blessed":{}}]}`)},
		{Name: "blessed-entry-not-object", Break: blessedWith(`{"comments":[{"post":"p.md","blessed":["x"]}]}`)},
		{Name: "blessed-entry-no-url", Break: blessedWith(`{"comments":[{"post":"p.md","blessed":[{"blessed_at":"2026-01-15T10:00:00Z"}]}]}`)},
		{Name: "blessed-many-problems", Break: blessedWith(`{"comments":[{"blessed":[{}]},"x",{"post":"p.md"}]}`)},
		{Name: "following-invalid-json", Break: followingWith("{not json")},
		{Name: "following-no-following", Break: followingWith(`{"version":"x"}`)},
		{Name: "following-not-array", Break: followingWith(`{"following":{}}`)},
		{Name: "following-entry-not-object", Break: followingWith(`{"following":["x"]}`)},
		{Name: "following-entry-no-added-at", Break: followingWith(`{"following":[{"url":"https://a.example"}]}`)},
		{Name: "following-many-problems", Break: followingWith(`{"following":[{},"x"]}`)},
		{Name: "posts-ok", Break: func(t testing.TB, s *Site) {
			s.SignedMarkdown(t, "content/pub.polis.core/post/20260115/hello.md", s.PrivateKey(t), "Hello.\n", published, "", false)
			s.SignedMarkdown(t, "content/pub.polis.core/comment/20260115/reply.md", s.PrivateKey(t), "A reply.\n", published, s.Domain(), true)
		}},
		{Name: "post-tampered", Break: func(t testing.TB, s *Site) {
			rel := "content/pub.polis.core/post/20260115/hello.md"
			s.SignedMarkdown(t, rel, s.PrivateKey(t), "Hello.\n", published, "", false)
			s.Write(t, rel, s.Read(t, rel)+"Tampered.\n", 0644)
		}},
		{Name: "comment-foreign-signature", Break: func(t testing.TB, s *Site) {
			stranger, _, err := signing.GenerateKeypair()
			if err != nil {
				t.Fatal(err)
			}
			s.SignedMarkdown(t, "content/pub.polis.core/comment/20260115/reply.md", stranger, "A reply.\n", published, s.Domain(), true)
		}},
		{Name: "suspicious-files", Break: func(t testing.TB, s *Site) {
			s.Write(t, "site/tool.sh", "echo\n", 0644)
			s.Write(t, "site/run.txt", "x\n", 0755)
			s.Write(t, "site/readonly.txt", "x\n", 0444)
			if err := os.Symlink("readonly.txt", s.Path("site/link.txt")); err != nil {
				t.Fatal(err)
			}
		}},

		// ── patrol.upgrade / patrol.policy_format_drift / patrol.warn.policy
		{Name: "bundle-missing-theme", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, bundleJSON, func(m map[string]any) { delete(m["themes"].(map[string]any), "zane") })
		}},
		{Name: "bundle-type-dir-drift", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, bundleJSON, func(m map[string]any) {
				typeOf(m, "pub.polis.post")["dir"] = "elsewhere"
			})
		}},
		{Name: "bundle-type-emits-drift", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, bundleJSON, func(m map[string]any) {
				ty := typeOf(m, "pub.polis.tag")
				emits, _ := ty["emits"].([]any)
				if len(emits) == 0 {
					t.Fatal("fixture: pub.polis.tag declares no emits to drop")
				}
				ty["emits"] = emits[1:]
			})
		}},
		{Name: "policy-no-deny-all", Break: func(t testing.TB, s *Site) {
			s.Write(t, pubPolicy, strings.Replace(s.Read(t, pubPolicy), `{"active":true,"policy":"deny all from all"}`+"\n", "", 1), 0644)
		}},
		{Name: "policy-private-drift", Break: func(t testing.TB, s *Site) {
			s.Write(t, privPolicy, s.Read(t, privPolicy)+`{"active":true,"policy":"deny pub.polis.dm from all"}`+"\n", 0600)
		}},
		{Name: "policy-format-v1", Break: func(t testing.TB, s *Site) {
			s.Write(t, privPolicy, strings.Replace(s.Read(t, privPolicy), `"version":2`, `"version":1`, 1), 0600)
		}},
		{Name: "policy-duplicate-rule", Break: func(t testing.TB, s *Site) {
			s.Write(t, pubPolicy, s.Read(t, pubPolicy)+`{"active":true,"policy":"allow pub.polis.dm from following"}`+"\n", 0644)
		}},
		{Name: "policy-public-missing", Break: func(t testing.TB, s *Site) { s.Remove(t, pubPolicy) }},
		{Name: "policy-private-missing", Break: func(t testing.TB, s *Site) { s.Remove(t, privPolicy) }},

		// ── snapshot-based alerts: a baseline on sweep 1, the change on sweep 2
		{Name: "salt-tampered", Mutate: func(t testing.TB, s *Site) {
			s.Write(t, ".polis/storage-salt", strings.Repeat("cd", 32), 0600)
			s.Touch(t, ".polis/storage-salt", T0)
		}},
		{Name: "salt-touched", Mutate: func(t testing.TB, s *Site) { s.Touch(t, ".polis/storage-salt", T1) }},
		{Name: "salt-missing", Break: func(t testing.TB, s *Site) { s.Remove(t, ".polis/storage-salt") }},
		{Name: "key-mtimes", Mutate: func(t testing.TB, s *Site) {
			s.Touch(t, ".polis/keys/id_ed25519", T1)
			s.Touch(t, ".polis/keys/id_ed25519.pub", T1)
			s.Touch(t, wellKnown, T1)
		}},
		{Name: "files-created", Mutate: func(t testing.TB, s *Site) {
			s.Write(t, ".polis/keys/id_extra", "x\n", 0600)
			s.Write(t, "policies/extra.jsonl", "\n", 0644)
			s.Write(t, ".polis/webapp/hooks/post-publish", "#!/bin/sh\n", 0700)
		}},

		// ── licence
		{Name: "license-intact", Break: func(t testing.TB, s *Site) { stateLicense(t, s, true) }},
		{Name: "license-tampered", Break: func(t testing.TB, s *Site) {
			stateLicense(t, s, true)
			rel := relTo(t, s, site.LicensePath(s.Dir))
			s.Write(t, rel, strings.Replace(s.Read(t, rel), `"train-ai": "n"`, `"train-ai": "y"`, 1), 0644)
		}},
		{Name: "license-dangling", Break: func(t testing.TB, s *Site) {
			stateLicense(t, s, true)
			s.Remove(t, relTo(t, s, site.LicensePath(s.Dir)))
		}},
		{Name: "license-no-pages", Break: func(t testing.TB, s *Site) { stateLicense(t, s, false) }},
		// Two further states of the published-key change: the licence is
		// verified against the PUBLISHED key, so it verifies with no key file
		// (A), and cannot verify with no published key (B).
		{Name: "license-key-file-missing", Break: func(t testing.TB, s *Site) {
			stateLicense(t, s, true)
			s.Remove(t, ".polis/keys/id_ed25519.pub")
		}},
		{Name: "license-no-published-key", Break: func(t testing.TB, s *Site) {
			stateLicense(t, s, true)
			s.EditJSON(t, wellKnown, func(m map[string]any) { delete(m, "public_key") })
		}},

		// ── content sensors
		{Name: "foreign-content", Break: func(t testing.TB, s *Site) {
			s.SignedMarkdown(t, "content/pub.polis.core/post/20260115/theirs.md", s.PrivateKey(t), "Not mine.\n", published, "other.polis.pub", false)
		}},
		{Name: "zeroed-envelope", Break: func(t testing.TB, s *Site) {
			rel := "content/pub.polis.core/post/20260115/zeroed.md"
			s.SignedMarkdown(t, rel, s.PrivateKey(t), "Signed by 0.59.0.\n", published, "", false)
			body := s.Read(t, rel)
			i := strings.Index(body, "signature: ")
			j := strings.Index(body[i:], "\n")
			bare := body[i+len("signature: ") : i+j]
			s.Write(t, rel, strings.Replace(body, bare, zeroedEnvelope(t, bare), 1), 0644)
		}},
		{Name: "dm-legacy-layout", Break: func(t testing.TB, s *Site) {
			s.Mkdir(t, ".polis/bundles/pub.polis.core/dm/conv", 0700)
			s.Write(t, ".polis/bundles/pub.polis.core/dm/conversations.json", "{}\n", 0600)
		}},
		{Name: "attestation-malformed", Break: func(t testing.TB, s *Site) {
			s.Write(t, "content/pub.polis.core/attestation/broken.json", "{not json", 0644)
		}},

		// ── containers
		{Name: "foreign-bundle", Break: func(t testing.TB, s *Site) {
			s.Mkdir(t, ".polis/bundles/org.example.other", 0700)
		}},
		{Name: "foreign-ds-domain", Break: func(t testing.TB, s *Site) {
			s.Mkdir(t, ".polis/ds/rogue.example/pub.polis.core/state", 0700)
		}},

		// ── the rest of the provisioning checks (no hosted event; Medic heals them)
		{Name: "dm-conversations-missing", Break: func(t testing.TB, s *Site) { s.Remove(t, convDir) }},
		{Name: "dm-conversations-unsafe", Break: func(t testing.TB, s *Site) { s.Chmod(t, convDir, 0755) }},
		{Name: "dm-keyring-missing", Break: func(t testing.TB, s *Site) {
			s.Remove(t, ".polis/bundles/pub.polis.core/dm/keyring.json")
		}},
		{Name: "tag-dir-missing", Break: func(t testing.TB, s *Site) { s.Remove(t, "content/pub.polis.core/tag") }},
		{Name: "avatar-missing", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) { delete(m, "avatar") })
		}},
		{Name: "avatar-incomplete", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) { m["avatar"].(map[string]any)["fg"] = "" })
		}},
		{Name: "favicon-missing", Break: func(t testing.TB, s *Site) { s.Remove(t, "favicon.svg") }},
		{Name: "favicon-stale", Break: func(t testing.TB, s *Site) {
			s.Write(t, "favicon.svg", `<svg><rect rx="22"/></svg>`+"\n", 0644)
		}},
		{Name: "webapp-view-mode", Break: func(t testing.TB, s *Site) {
			s.Write(t, ".polis/webapp/config.json", `{"view_mode":"list"}`+"\n", 0600)
		}},
		{Name: "stale-registration-checked", Break: func(t testing.TB, s *Site) {
			s.Write(t, ".polis/ds/"+DSDomain+"/registration_checked", "", 0600)
		}},
		{Name: "stale-registered-json", Break: func(t testing.TB, s *Site) {
			s.Write(t, ".polis/ds/"+DSDomain+"/registered.json", "{}\n", 0600)
		}},
		{Name: "stale-scoped-feed", Break: func(t testing.TB, s *Site) {
			s.Write(t, dsState+"/pub.polis.feed.me.jsonl", "", 0600)
		}},
		{Name: "stale-feed-viewed-at", Break: func(t testing.TB, s *Site) {
			s.Write(t, dsState+"/cursors.json", `{"cursors":{"pub.polis.feed.viewed_at":"2026-01-01T00:00:00Z"}}`+"\n", 0600)
		}},
		{Name: "wk-legacy-author", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) { m["author"] = "Old Name" })
		}},
		{Name: "wk-legacy-author-only", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) {
				m["author"] = m["author_name"]
				delete(m, "author_name")
			})
		}},
		{Name: "wk-author-name-empty", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) { m["author_name"] = "" })
		}},
		{Name: "wk-core-path-missing", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) {
				m["bundles"].(map[string]any)["pub.polis.core"] = map[string]any{"path": "content/nowhere/bundle.json"}
			})
		}},
		{Name: "wk-core-path-empty", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) {
				m["bundles"].(map[string]any)["pub.polis.core"] = map[string]any{"path": ""}
			})
		}},
		{Name: "wk-foreign-bundle-path", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) {
				m["bundles"].(map[string]any)["org.example.other"] = map[string]any{"path": "content/org.example.other/bundle.json"}
			})
		}},
		{Name: "wk-no-bundles", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) { m["bundles"] = map[string]any{} })
		}},
		{Name: "wk-invalid-public-key", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) { m["public_key"] = "ssh-ed25519 notakey" })
		}},
		{Name: "wk-active-theme", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) { m["active_theme"] = "vice" })
		}},
		{Name: "wk-bundle-active-field", Break: func(t testing.TB, s *Site) {
			s.EditJSON(t, wellKnown, func(m map[string]any) {
				m["bundles"].(map[string]any)["pub.polis.core"].(map[string]any)["active"] = true
			})
		}},
		{Name: "registry-malformed", Break: func(t testing.TB, s *Site) { s.Write(t, registry, "{not json", 0600) }},
		{Name: "registry-future-schema", Break: registryEdit(func(m map[string]any) { m["schema_version"] = 99 })},
		{Name: "registry-fractional-schema", Break: registryEdit(func(m map[string]any) { m["schema_version"] = 2.5 })},
		{Name: "registry-theme-bare", Break: registryEdit(func(m map[string]any) { m["active_theme"] = "vice" })},
		{Name: "registry-theme-gibberish", Break: registryEdit(func(m map[string]any) { m["active_theme"] = "not a theme" })},
		{Name: "registry-theme-dangling", Break: registryEdit(func(m map[string]any) { m["active_theme"] = "pub.polis.themes.nope" })},
		{Name: "registry-theme-empty", Break: registryEdit(func(m map[string]any) { m["active_theme"] = "" })},
		{Name: "registry-studio13-v3", Break: registryEdit(func(m map[string]any) {
			m["active_theme"] = "pub.polis.themes.studio13"
			m["active_shape"] = "pub.polis.shapes.v3"
		})},
		{Name: "registry-shape-bare", Break: registryEdit(func(m map[string]any) { m["active_shape"] = "v4" })},
		{Name: "registry-shape-dangling", Break: registryEdit(func(m map[string]any) { m["active_shape"] = "pub.polis.shapes.v9" })},
		{Name: "registry-payload-stale", Break: registryEdit(func(m map[string]any) {
			m["installed_bundles"].([]any)[0].(map[string]any)["shape_versions"].(map[string]any)["v4"] = "0.0.1"
		})},
		{Name: "payload-bytes-drift", Break: func(t testing.TB, s *Site) {
			rel := ".polis/bundles/pub.polis.core/shapes/v4/stream.css"
			s.Write(t, rel, s.Read(t, rel)+"/* hand edit */\n", 0644)
		}},
		{Name: "orphan-theme-dir", Break: func(t testing.TB, s *Site) { s.Mkdir(t, themesDir+"/retired", 0755) }},
		{Name: "legacy-content-path", Break: func(t testing.TB, s *Site) {
			s.Mkdir(t, ".polis/content/pub.polis.core", 0700)
		}},
		{Name: "legacy-theme-location", Break: func(t testing.TB, s *Site) { s.Mkdir(t, "site/themes", 0755) }},

		// ── the key file and the published key disagree.
		// Everything is signed by the PUBLISHED key; the key file holds another.
		{Name: C1Tenant, Break: func(t testing.TB, s *Site) {
			priv := s.PrivateKey(t)
			s.SignedMarkdown(t, "content/pub.polis.core/post/20260115/hello.md", priv, "Hello.\n", published, "", false)
			s.SignedMarkdown(t, "content/pub.polis.core/comment/20260115/reply.md", priv, "A reply.\n", published, s.Domain(), true)
			stateLicense(t, s, true)
			otherPriv, otherPub, err := signing.GenerateKeypair()
			if err != nil {
				t.Fatal(err)
			}
			s.Write(t, ".polis/keys/id_ed25519", string(otherPriv), 0600)
			s.Write(t, ".polis/keys/id_ed25519.pub", string(otherPub), 0644)
		}},
	}
}

func blessedWith(content string) func(testing.TB, *Site) {
	return func(t testing.TB, s *Site) { s.Write(t, blessed, content, 0644) }
}

func followingWith(content string) func(testing.TB, *Site) {
	return func(t testing.TB, s *Site) { s.Write(t, following, content, 0644) }
}

func registryEdit(edit func(m map[string]any)) func(testing.TB, *Site) {
	return func(t testing.TB, s *Site) { s.EditJSON(t, registry, edit) }
}

func typeOf(bundle map[string]any, name string) map[string]any {
	return bundle["types"].(map[string]any)[name].(map[string]any)
}

// stateLicense states reserved terms signed by the site's current key and,
// withPages, publishes the two pages the terms point at.
func stateLicense(t testing.TB, s *Site, withPages bool) {
	t.Helper()
	if _, err := site.StateLicense(s.Dir, "reserved", "https://"+s.Domain(), s.PrivateKey(t)); err != nil {
		t.Fatalf("state licence: %v", err)
	}
	if !withPages {
		return
	}
	mount := site.LicenseMountDir(s.Dir)
	for _, name := range []string{"index.html", "reserved-1.html"} {
		s.Write(t, mount+"/"+name, "<html></html>\n", 0644)
	}
}

func relTo(t testing.TB, s *Site, abs string) string {
	t.Helper()
	rel := strings.TrimPrefix(abs, s.Dir+string(os.PathSeparator))
	if rel == abs {
		t.Fatalf("%s is not under %s", abs, s.Dir)
	}
	return rel
}
